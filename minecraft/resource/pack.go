package resource

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tailscale/hujson"
)

type Pack interface {
	fmt.Stringer
	io.ReaderAt
	io.WriterTo
	fs.FS

	UUID() string
	Version() string
	Name() string
	Checksum() [32]byte
	Modules() []Module
	Description() string
	Dependencies() []Dependency
	BaseDir() string
	CanRead() bool

	WithContentKey(key string) Pack
	HasBehaviours() bool
	HasWorldTemplate() bool
	HasTextures() bool
	HasScripts() bool
	Encrypted() bool
	DownloadURL() string
	ContentKey() string
	Manifest() Manifest
	Len() int
	DataChunkCount(packChunkSize int) int
}

// Pack is a container of a resource pack parsed from a directory or a .zip archive (or .mcpack). It holds
// methods that may be used to get information about the resource pack.
type pack struct {
	// manifest is the manifest of the resource pack. It contains information about the pack such as the name,
	// version and description.
	manifest *Manifest

	// downloadURL is the URL that the resource pack can be downloaded from. If the string is empty, then the
	// resource pack will be downloaded over RakNet rather than HTTP.
	downloadURL string
	// contentKey is the key used to encrypt the files. The client uses this to decrypt the resource pack if encrypted.
	// If nothing is encrypted, this field can be left as an empty string.
	contentKey string

	baseDir string
	fs      fs.FS

	createZip func() error
	reader    io.ReaderAt
	size      int
	// checksum is the SHA256 checksum of the full content of the file. It is sent to the client so that it
	// can 'verify' the download.
	checksum [32]byte
}

// ReadPath compiles a resource pack found at the path passed. The resource pack must either be a zip archive
// (extension does not matter, could be .zip or .mcpack), or a directory containing a resource pack. In the
// case of a directory, the directory is compiled into an archive and the pack is parsed from that.
// ReadPath operates assuming the resource pack has a 'manifest.json' file in it. If it does not, the function
// will fail and return an error.
func ReadPath(path string) (Pack, error) {
	return compile(path)
}

// ReadURL downloads a resource pack found at the URL passed and compiles it. The resource pack must be a valid
// zip archive where the manifest.json file is inside a subdirectory rather than the root itself. If the resource
// pack is not a valid zip or there is no manifest.json file, an error is returned.
func ReadURL(client *http.Client, url string) (Pack, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download resource pack: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download resource pack: %v (%d)", resp.Status, resp.StatusCode)
	}
	pack_, err := Read(resp.Body)
	if err != nil {
		return nil, err
	}
	(pack_.(*pack)).downloadURL = url
	return pack_, nil
}

// MustReadPath compiles a resource pack found at the path passed. The resource pack must either be a zip
// archive (extension does not matter, could be .zip or .mcpack), or a directory containing a resource pack.
// In the case of a directory, the directory is compiled into an archive and the pack is parsed from that.
// ReadPath operates assuming the resource pack has a 'manifest.json' file in it. If it does not, the function
// will fail and return an error.
// Unlike ReadPath, MustReadPath does not return an error and panics if an error occurs instead.
func MustReadPath(path string) Pack {
	pack, err := compile(path)
	if err != nil {
		panic(err)
	}
	return pack
}

// MustReadURL downloads a resource pack found at the URL passed and compiles it. The resource pack must be a valid
// zip archive where the manifest.json file is inside a subdirectory rather than the root itself. If the resource
// pack is not a valid zip or there is no manifest.json file, an error is returned.
// Unlike ReadURL, MustReadURL does not return an error and panics if an error occurs instead.
func MustReadURL(url string) Pack {
	pack, err := ReadURL(http.DefaultClient, url)
	if err != nil {
		panic(err)
	}
	return pack
}

// Read parses an archived resource pack written to a raw byte slice passed. The data must be a valid
// zip archive and contain a pack manifest in order for the function to succeed.
// Read saves the data to a temporary archive.
func Read(r io.Reader) (Pack, error) {
	temp, err := createTempFile()
	if err != nil {
		return nil, fmt.Errorf("create temp zip archive: %w", err)
	}
	n, err := io.Copy(temp, r)
	if err != nil {
		return nil, fmt.Errorf("read pack: %w", err)
	}
	return compileReader(temp, n)
}

// FromReaderAt reads a Pack from this reader, this reader must not be closed for as long as the pack is used
func FromReaderAt(r io.ReaderAt, size int64) (Pack, error) {
	return compileReader(r, size)
}

func (pack *pack) CanRead() bool {
	return !pack.Encrypted()
}

// Name returns the name of the resource pack.
func (pack *pack) Name() string {
	return pack.manifest.Header.Name
}

// UUID returns the UUID of the resource pack.
func (pack *pack) UUID() string {
	return pack.manifest.Header.UUID
}

// Description returns the description of the resource pack.
func (pack *pack) Description() string {
	return pack.manifest.Header.Description
}

// Version returns the string version of the resource pack. It is guaranteed to have 3 digits in it, joined
// by a dot.
func (pack *pack) Version() string {
	return pack.manifest.Header.Version.String()
}

// Modules returns all modules that the resource pack exists out of. Resource packs usually have only one
// module, but may have more depending on their functionality.
func (pack *pack) Modules() []Module {
	return pack.manifest.Modules
}

// Dependencies returns all dependency resource packs that must be loaded in order for this resource pack to
// function correctly.
func (pack *pack) Dependencies() []Dependency {
	return pack.manifest.Dependencies
}

func (pack *pack) BaseDir() string {
	return pack.baseDir
}

// HasScripts checks if any of the modules of the resource pack have the type 'client_data', meaning they have
// scripts in them.
func (pack *pack) HasScripts() bool {
	for _, module := range pack.manifest.Modules {
		if module.Type == "client_data" {
			// The module has the client_data type, meaning it holds client scripts.
			return true
		}
	}
	return false
}

// HasBehaviours checks if any of the modules of the resource pack have either the type 'data' or
// 'client_data', meaning they contain behaviours (or scripts).
func (pack *pack) HasBehaviours() bool {
	for _, module := range pack.manifest.Modules {
		if module.Type == "client_data" || module.Type == "data" {
			// The module has the client_data or data type, meaning it holds behaviours.
			return true
		}
	}
	return false
}

// HasTextures checks if any of the modules of the resource pack have the type 'resources', meaning they have
// textures in them.
func (pack *pack) HasTextures() bool {
	for _, module := range pack.manifest.Modules {
		if module.Type == "resources" {
			// The module has the resources type, meaning it holds textures.
			return true
		}
	}
	return false
}

// HasWorldTemplate checks if the resource compiled holds a level.dat in it, indicating that the resource is
// a world template.
func (pack *pack) HasWorldTemplate() bool {
	return pack.manifest.worldTemplate
}

// DownloadURL returns the URL that the resource pack can be downloaded from. If the string is empty, then the
// resource pack will be downloaded over RakNet rather than HTTP.
func (pack *pack) DownloadURL() string {
	return pack.downloadURL
}

// Checksum returns the SHA256 checksum made from the full, compressed content of the resource pack archive.
// It is transmitted as a string over network.
func (pack *pack) Checksum() [32]byte {
	pack.createZip()
	return pack.checksum
}

// Len returns the total length in bytes of the content of the archive that contained the resource pack.
func (pack *pack) Len() int {
	pack.createZip()
	return pack.size
}

// DataChunkCount returns the amount of chunks the data of the resource pack is split into if each chunk has
// a specific length.
func (pack *pack) DataChunkCount(length int) int {
	count := pack.Len() / length
	if pack.Len()%length != 0 {
		count++
	}
	return count
}

// Encrypted returns if the resource pack has been encrypted with a content key or not.
func (pack *pack) Encrypted() bool {
	return pack.contentKey != ""
}

// ContentKey returns the encryption key used to encrypt the resource pack. If the pack is not encrypted then
// this can be empty.
func (pack *pack) ContentKey() string {
	return pack.contentKey
}

// ReadAt reads len(b) bytes from the resource pack's archive data at offset off and copies it into b. The
// amount of bytes read n is returned.
func (pack *pack) ReadAt(b []byte, off int64) (n int, err error) {
	err = pack.createZip()
	if err != nil {
		return 0, err
	}

	return pack.reader.ReadAt(b, off)
}

// WriteTo writes the packs zip data to the writer
func (pack *pack) WriteTo(w io.Writer) (n int64, err error) {
	var buf = make([]byte, 0x1000)
	off := int64(0)
	for {
		n, err := pack.ReadAt(buf, int64(off))
		off += int64(n)
		if n > 0 {
			_, err = w.Write(buf[:n])
			if err != nil {
				return off, err
			}
		}
		if err != nil {
			if err == io.EOF {
				return off, nil
			}
			return off, err
		}
	}
}

// WithContentKey creates a copy of the pack and sets the encryption key to the key provided, after which the
// new Pack is returned.
func (pack pack) WithContentKey(key string) Pack {
	pack.contentKey = key
	return &pack
}

// Manifest returns the manifest found in the manifest.json of the resource pack. It contains information
// about the pack such as its name.
func (pack *pack) Manifest() Manifest {
	return *pack.manifest
}

// String returns a readable representation of the resource pack. It implements the Stringer interface.
func (pack *pack) String() string {
	return fmt.Sprintf("%v v%v (%v): %v", pack.Name(), pack.Version(), pack.UUID(), pack.Description())
}

func (pack *pack) Open(name string) (fs.File, error) {
	return pack.fs.Open(name)
}

// compile compiles the resource pack found in path, either a zip archive or a directory, and returns a
// resource pack if successful.
func compile(path string) (*pack, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open resource pack path: %w", err)
	}
	if info.IsDir() {
		return compileFS(os.DirFS(path))
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open resource pack path: %w", err)
	}
	stat, _ := f.Stat()
	fSize := stat.Size()
	return compileReader(f, fSize)
}

func compileReader(r io.ReaderAt, fSize int64) (*pack, error) {
	zr, err := zip.NewReader(r, fSize)
	if err != nil {
		return nil, fmt.Errorf("error opening zip reader: %v", err)
	}

	// if there is only 1 zip file open it and return it instead
	if len(zr.File) == 1 && strings.HasSuffix(zr.File[0].Name, ".zip") {
		zrf := zr.File[0]
		if zrf.Method == zip.Store {
			offset, err := zrf.DataOffset()
			if err != nil {
				return nil, err
			}
			return compileReader(io.NewSectionReader(r, offset, int64(zrf.CompressedSize64)), int64(zrf.CompressedSize64))
		} else {
			zf, err := zrf.Open()
			if err != nil {
				return nil, err
			}
			defer zf.Close()
			temp, err := createTempFile()
			if err != nil {
				return nil, err
			}
			size, err := io.Copy(temp, zf)
			if err != nil {
				return nil, err
			}
			return compileReader(temp, size)
		}
	}

	pack, err := compileFS(zr)
	if err != nil {
		return nil, err
	}

	pack.createZip = func() error { return nil }
	pack.reader = r
	pack.size = int(fSize)

	h := sha256.New()
	_, err = pack.WriteTo(h)
	if err != nil {
		return nil, fmt.Errorf("read resource pack file content: %w", err)
	}
	pack.checksum = [32]byte(h.Sum(nil))
	return pack, nil
}

func compileFS(fsys fs.FS) (*pack, error) {
	// First we read the manifest to ensure that it exists and is valid.
	reader := packReader{fs: fsys}
	manifest, baseDir, err := reader.readManifest()
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var fsys2 = fsys
	if baseDir != "." {
		fsys2, err = fs.Sub(fsys, baseDir)
		if err != nil {
			return nil, fmt.Errorf("fs sub: %w", err)
		}
	}

	pack := &pack{
		manifest: manifest,
		baseDir:  baseDir,
		fs:       fsys2,
	}
	pack.createZip = sync.OnceValue(func() (err error) {
		pack.reader, pack.size, pack.checksum, err = createTempArchive(fsys)
		return err
	})

	return pack, nil
}

// packReader wraps around a zip.Reader to provide file finding functionality.
type packReader struct {
	fs fs.FS
}

// find attempts to find a file in a zip reader. If found, it returns an Open()ed reader of the file that may
// be used to read data from the file.
func (reader packReader) find(fileName string) (io.ReadCloser, string, error) {
	fileReader, err := reader.fs.Open(fileName)
	if err == nil {
		return fileReader, fileName, nil
	}

	fileNames, err := fs.Glob(reader.fs, "*/"+fileName)
	if err != nil {
		return nil, "", err
	}

	if len(fileNames) == 0 {
		return nil, "", fmt.Errorf("'%v' not found in zip", fileName)
	}

	fileName = fileNames[0]
	fileReader, err = reader.fs.Open(fileName)
	if err != nil {
		return nil, "", err
	}
	return fileReader, fileName, nil
}

func parseJson(s []byte, out any) error {
	v, err := hujson.Parse(s)
	if err != nil {
		if !strings.Contains(err.Error(), "after top-level value") {
			return err
		}
	}
	v.Standardize()

	d := json.NewDecoder(bytes.NewReader(v.Pack()))
	err = d.Decode(&out)
	if err != nil {
		return err
	}
	return nil
}

// readManifest reads the manifest from the resource pack located at the path passed. If not found in the root
// of the resource pack, it will also attempt to find it deeper down into the archive.
func (reader packReader) readManifest() (*Manifest, string, error) {
	// Try to find the manifest file in the zip.
	manifestFile, name, err := reader.find("manifest.json")
	if err != nil {
		return nil, "", fmt.Errorf("error loading manifest: %v", err)
	}
	baseDir := filepath.Dir(name)
	defer func() {
		_ = manifestFile.Close()
	}()

	// Read all data from the manifest file so that we can decode it into a Manifest struct.
	allData, err := io.ReadAll(manifestFile)
	if err != nil {
		return nil, "", fmt.Errorf("error reading from manifest file: %v", err)
	}

	manifest := Manifest{}
	if err := parseJson(allData, &manifest); err != nil {
		return nil, "", fmt.Errorf("error decoding manifest JSON: %v (data: %v)", err, string(allData))
	}
	manifest.Header.UUID = strings.ToLower(manifest.Header.UUID)

	if _, _, err := reader.find("level.dat"); err == nil {
		manifest.worldTemplate = true
	}

	return &manifest, baseDir, nil
}
