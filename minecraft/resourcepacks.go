package minecraft

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"github.com/sandertv/gophertunnel/minecraft/resource"
)

type ResourcePackHandler interface {
	OnResourcePacksInfo(*packet.ResourcePacksInfo) error
	OnResourcePackClientResponse(*packet.ResourcePackClientResponse) error
	OnResourcePackDataInfo(*packet.ResourcePackDataInfo) error
	OnResourcePackChunkRequest(*packet.ResourcePackChunkRequest) error
	OnResourcePackChunkData(*packet.ResourcePackChunkData) error
	OnResourcePackStack(*packet.ResourcePackStack) error
	GetResourcePacksInfo(bool) *packet.ResourcePacksInfo
	ResourcePacks() []resource.Pack
}

type defaultResourcepackHandler struct {
	c         *Conn
	packQueue *resourcePackQueue
	packMu    sync.Mutex

	// resourcePacks is a slice of resource packs that the listener may hold. Each client will be asked to
	// download these resource packs upon joining.
	resourcePacks []resource.Pack

	// ignoredResourcePacks is a slice of resource packs that are not being downloaded due to the downloadResourcePack
	// func returning false for the specific pack.
	ignoredResourcePacks []exemptedResourcePack
}

func (r *defaultResourcepackHandler) ResourcePacks() []resource.Pack {
	return r.resourcePacks
}

// OnResourcePacksInfo handles a ResourcePacksInfo packet sent by the server. The client responds by
// sending the packs it needs downloaded.
func (r *defaultResourcepackHandler) OnResourcePacksInfo(pk *packet.ResourcePacksInfo) error {
	// First create a new resource pack queue with the information in the packet so we can download them
	// properly later.
	r.packQueue = &resourcePackQueue{
		packAmount:       len(pk.TexturePacks),
		downloadingPacks: make(map[uuid.UUID]downloadingPack),
		awaitingPacks:    make(map[uuid.UUID]*downloadingPack),
	}
	packsToDownload := make([]string, 0, len(pk.TexturePacks))

	for index, pack := range pk.TexturePacks {
		if _, ok := r.packQueue.downloadingPacks[pack.UUID]; ok {
			r.c.log.Warn("duplicate texture pack in resource pack info", "UUID", pack.UUID)
			r.packQueue.packAmount--
			continue
		}
		if r.c.downloadResourcePack != nil && !r.c.downloadResourcePack(pack.UUID, pack.Version, index, len(pk.TexturePacks)) {
			r.ignoredResourcePacks = append(r.ignoredResourcePacks, exemptedResourcePack{
				uuid:    pack.UUID,
				version: pack.Version,
			})
			r.packQueue.packAmount--
			continue
		}
		// This UUID_Version is a hack Mojang put in place.
		packsToDownload = append(packsToDownload, pack.UUID.String()+"_"+pack.Version)
		r.packQueue.downloadingPacks[pack.UUID] = downloadingPack{
			size:       pack.Size,
			buf:        bytes.NewBuffer(make([]byte, 0, pack.Size)),
			newFrag:    make(chan []byte),
			contentKey: pack.ContentKey,
		}
	}

	if len(packsToDownload) != 0 {
		r.c.expect(packet.IDResourcePackDataInfo, packet.IDResourcePackChunkData)
		_ = r.c.WritePacket(&packet.ResourcePackClientResponse{
			Response:        packet.PackResponseSendPacks,
			PacksToDownload: packsToDownload,
		})
		return nil
	}
	r.c.expect(packet.IDResourcePackStack)

	_ = r.c.WritePacket(&packet.ResourcePackClientResponse{Response: packet.PackResponseAllPacksDownloaded})
	return nil
}

// OnResourcePackDataInfo handles a resource pack data info packet, which initiates the downloading of the
// pack by the client.
func (r *defaultResourcepackHandler) OnResourcePackDataInfo(pk *packet.ResourcePackDataInfo) error {
	id, err := uuid.Parse(strings.Split(pk.UUID, "_")[0])
	if err != nil {
		return err
	}

	pack, ok := r.packQueue.downloadingPacks[id]
	if !ok {
		// We either already downloaded the pack or we got sent an invalid UUID, that did not match any pack
		// sent in the ResourcePacksInfo packet.
		return fmt.Errorf("unknown pack to download with UUID %v", id)
	}
	if pack.size != pk.Size {
		// Size mismatch: The ResourcePacksInfo packet had a size for the pack that did not match with the
		// size sent here.
		r.c.log.Warn("different size in the ResourcePacksInfo packet than the ResourcePackDataInfo packet", "UUID", pk.UUID)
		pack.size = pk.Size
	}

	// Remove the resource pack from the downloading packs and add it to the awaiting packets.
	delete(r.packQueue.downloadingPacks, id)
	r.packQueue.awaitingPacks[id] = &pack

	pack.chunkSize = pk.DataChunkSize

	// The client calculates the chunk count by itself: You could in theory send a chunk count of 0 even
	// though there's data, and the client will still download normally.
	chunkCount := uint32(pk.Size / uint64(pk.DataChunkSize))
	if pk.Size%uint64(pk.DataChunkSize) != 0 {
		chunkCount++
	}

	idCopy := pk.UUID
	go func() {
		for i := uint32(0); i < chunkCount; i++ {
			_ = r.c.WritePacket(&packet.ResourcePackChunkRequest{
				UUID:       idCopy,
				ChunkIndex: i,
			})
			select {
			case <-r.c.ctx.Done():
				return
			case frag := <-pack.newFrag:
				// Write the fragment to the full buffer of the downloading resource pack.
				_, _ = pack.buf.Write(frag)
			}
		}
		r.packMu.Lock()
		defer r.packMu.Unlock()

		if pack.buf.Len() != int(pack.size) {
			r.c.log.Warn("incorrect resource pack size", "expected", pack.size, "got", pack.buf.Len())
			return
		}
		// First parse the resource pack from the total byte buffer we obtained.
		newPack, err := resource.Read(pack.buf)
		if err != nil {
			r.c.log.Warn("invalid full resource pack data", "UUID", id, "err", err)
			return
		}
		r.packQueue.packAmount--
		// Finally we add the resource to the resource packs slice.
		r.resourcePacks = append(r.resourcePacks, newPack.WithContentKey(pack.contentKey))
		if r.packQueue.packAmount == 0 {
			r.c.expect(packet.IDResourcePackStack)
			_ = r.c.WritePacket(&packet.ResourcePackClientResponse{Response: packet.PackResponseAllPacksDownloaded})
		}
	}()
	return nil
}

// OnChunkRequest handles a resource pack chunk request, which requests a part of the resource
// pack to be downloaded.
func (r *defaultResourcepackHandler) OnResourcePackChunkRequest(pk *packet.ResourcePackChunkRequest) error {
	current := r.packQueue.currentPack
	pkUuid, err := uuid.Parse(pk.UUID)
	if err != nil {
		return err
	}
	if current.UUID() != pkUuid {
		return fmt.Errorf("resource pack chunk request had unexpected UUID: expected %v, but got %v", current.UUID(), pk.UUID)
	}
	if r.packQueue.currentOffset != uint64(pk.ChunkIndex)*packChunkSize {
		return fmt.Errorf("resource pack chunk request had unexpected chunk index: expected %v, but got %v", r.packQueue.currentOffset/packChunkSize, pk.ChunkIndex)
	}
	response := &packet.ResourcePackChunkData{
		UUID:       pk.UUID,
		ChunkIndex: pk.ChunkIndex,
		DataOffset: r.packQueue.currentOffset,
		Data:       make([]byte, packChunkSize),
	}
	r.packQueue.currentOffset += packChunkSize
	// We read the data directly into the response's data.
	if n, err := current.ReadAt(response.Data, int64(response.DataOffset)); err != nil {
		// If we hit an EOF, we don't need to return an error, as we've simply reached the end of the content
		// AKA the last chunk.
		if err != io.EOF {
			return fmt.Errorf("error reading resource pack chunk: %v", err)
		}
		response.Data = response.Data[:n]

		defer func() {
			if !r.packQueue.AllDownloaded() {
				_ = r.nextResourcePackDownload()
			} else {
				r.c.expect(packet.IDResourcePackClientResponse)
			}
		}()
	}
	if err := r.c.WritePacket(response); err != nil {
		return fmt.Errorf("error writing resource pack chunk data packet: %v", err)
	}

	return nil
}

// OnResourcePackChunkData handles a resource pack chunk data packet, which holds a fragment of a resource
// pack that is being downloaded.
func (r *defaultResourcepackHandler) OnResourcePackChunkData(pk *packet.ResourcePackChunkData) error {
	pkUuid, err := uuid.Parse(strings.Split(pk.UUID, "_")[0])
	if err != nil {
		return err
	}
	pack, ok := r.packQueue.awaitingPacks[pkUuid]
	if !ok {
		// We haven't received a ResourcePackDataInfo packet from the server, so we can't use this data to
		// download a resource pack.
		return fmt.Errorf("resource pack chunk data for resource pack that was not being downloaded")
	}
	lastData := pack.buf.Len()+int(pack.chunkSize) >= int(pack.size)
	if !lastData && uint32(len(pk.Data)) != pack.chunkSize {
		// The chunk data didn't have the full size and wasn't the last data to be sent for the resource pack,
		// meaning we got too little data.
		return fmt.Errorf("resource pack chunk data had a length of %v, but expected %v", len(pk.Data), pack.chunkSize)
	}
	if pk.ChunkIndex != pack.expectedIndex {
		return fmt.Errorf("resource pack chunk data had chunk index %v, but expected %v", pk.ChunkIndex, pack.expectedIndex)
	}
	pack.expectedIndex++
	pack.newFrag <- pk.Data
	return nil
}

// nextResourcePackDownload moves to the next resource pack to download and sends a resource pack data info
// packet with information about it.
func (r *defaultResourcepackHandler) nextResourcePackDownload() error {
	pk, ok := r.packQueue.NextPack()
	if !ok {
		return fmt.Errorf("no resource packs to download")
	}
	if err := r.c.WritePacket(pk); err != nil {
		return fmt.Errorf("error sending resource pack data info packet: %v", err)
	}
	// Set the next expected packet to ResourcePackChunkRequest packets.
	r.c.expect(packet.IDResourcePackChunkRequest)
	return nil
}

// OnResourcePackStack handles a ResourcePackStack packet sent by the server. The stack defines the order
// that resource packs are applied in.
func (r *defaultResourcepackHandler) OnResourcePackStack(pk *packet.ResourcePackStack) error {
	// We currently don't apply resource packs in any way, so instead we just check if all resource packs in
	// the stacks are also downloaded.
	for _, pack := range pk.TexturePacks {
		if !r.hasPack(pack.UUID, pack.Version, false) {
			return fmt.Errorf("texture pack {uuid=%v, version=%v} not downloaded", pack.UUID, pack.Version)
		}
	}
	r.c.expect(packet.IDStartGame)
	_ = r.c.WritePacket(&packet.ResourcePackClientResponse{Response: packet.PackResponseCompleted})
	return nil
}

// hasPack checks if the connection has a resource pack downloaded with the UUID and version passed, provided
// the pack either has or does not have behaviours in it.
func (r *defaultResourcepackHandler) hasPack(id string, version string, hasBehaviours bool) bool {
	packId := uuid.MustParse(id)
	for _, exempted := range exemptedPacks {
		if exempted.uuid == packId && exempted.version == version {
			// The server may send this resource pack on the stack without sending it in the info, as the client
			// always has it downloaded.
			return true
		}
	}
	r.packMu.Lock()
	defer r.packMu.Unlock()

	for _, ignored := range r.ignoredResourcePacks {
		if ignored.uuid == packId && ignored.version == version {
			return true
		}
	}
	for _, pack := range r.resourcePacks {
		if pack.UUID() == packId && pack.Version() == version && pack.HasBehaviours() == hasBehaviours {
			return true
		}
	}
	return false
}

// packChunkSize is the size of a single chunk of data from a resource pack: 512 kB or 0.5 MB
const packChunkSize = 1024 * 128

// OnResourcePackClientResponse handles an incoming resource pack client response packet. The packet is
// handled differently depending on the response.
func (r *defaultResourcepackHandler) OnResourcePackClientResponse(pk *packet.ResourcePackClientResponse) error {
	switch pk.Response {
	case packet.PackResponseRefused:
		// Even though this response is never sent, we handle it appropriately in case it is changed to work
		// correctly again.
		return r.c.Close()
	case packet.PackResponseSendPacks:
		packs := pk.PacksToDownload
		r.packQueue = &resourcePackQueue{packs: r.resourcePacks}
		if err := r.packQueue.Request(packs); err != nil {
			return fmt.Errorf("error looking up resource packs to download: %v", err)
		}
		// Proceed with the first resource pack download. We run all downloads in sequence rather than in
		// parallel, as it's less prone to packet loss.
		if err := r.nextResourcePackDownload(); err != nil {
			return err
		}
	case packet.PackResponseAllPacksDownloaded:
		pk := &packet.ResourcePackStack{BaseGameVersion: protocol.CurrentVersion, Experiments: []protocol.ExperimentData{{Name: "cameras", Enabled: true}}}
		for _, pack := range r.resourcePacks {
			resourcePack := protocol.StackResourcePack{UUID: pack.UUID().String(), Version: pack.Version()}
			pk.TexturePacks = append(pk.TexturePacks, resourcePack)
		}
		for _, exempted := range exemptedPacks {
			pk.TexturePacks = append(pk.TexturePacks, protocol.StackResourcePack{
				UUID:    exempted.uuid.String(),
				Version: exempted.version,
			})
		}
		if err := r.c.WritePacket(pk); err != nil {
			return fmt.Errorf("error writing resource pack stack packet: %v", err)
		}
	case packet.PackResponseCompleted:
		r.c.loggedIn = true
	default:
		return fmt.Errorf("unknown resource pack client response: %v", pk.Response)
	}
	return nil
}

func (r *defaultResourcepackHandler) GetResourcePacksInfo(texturePacksRequired bool) *packet.ResourcePacksInfo {
	pk := &packet.ResourcePacksInfo{TexturePackRequired: texturePacksRequired}
	for _, pack := range r.ResourcePacks() {
		texturePack := protocol.TexturePackInfo{
			UUID:        pack.UUID(),
			Version:     pack.Version(),
			Size:        uint64(pack.Len()),
			DownloadURL: pack.DownloadURL(),
		}
		if pack.Encrypted() {
			texturePack.ContentKey = pack.ContentKey()
			texturePack.ContentIdentity = pack.Manifest().Header.UUID.String()
		}
		pk.TexturePacks = append(pk.TexturePacks, texturePack)
	}
	return pk
}
