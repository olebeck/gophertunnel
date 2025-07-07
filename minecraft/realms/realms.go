package realms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"golang.org/x/oauth2"
)

// Client is an instance of the realms api with a token.
type Client struct {
	ClientVersion string
	tokenSrc      oauth2.TokenSource
	xblToken      *auth.XBLToken
	baseUrl       string
	httpClient    *http.Client
}

// NewClient returns a new Client instance with the supplied token source for authentication.
// If httpClient is nil, http.DefaultClient will be used to request the realms api.
func NewClient(src oauth2.TokenSource, httpClient *http.Client, baseUrl string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if baseUrl == "" {
		baseUrl = "https://pocket.realms.minecraft.net/"
	}
	return &Client{
		tokenSrc:      src,
		httpClient:    httpClient,
		ClientVersion: protocol.CurrentVersion,
		baseUrl:       baseUrl,
	}
}

// Player is a player in a Realm.
type Player struct {
	UUID       string `json:"uuid"`
	Name       string `json:"Name"`
	Operator   bool   `json:"operator"`
	Accepted   bool   `json:"accepted"`
	Online     bool   `json:"online"`
	Permission string `json:"permission"`
}

// Realm is the realm structure returned from the api.
type Realm struct {
	// ID is the unique id for this realm.
	ID int `json:"id"`
	// RemoteSubscriptionID is The subscription ID of the realm.
	RemoteSubscriptionID string `json:"remoteSubscriptionID"`
	// Owner is always an empty string.
	Owner string `json:"owner"`
	// OwnerUUID is the XboxUserID (XUID) of the owner.
	OwnerUUID string `json:"ownerUUID"`
	// Name is the name of the Realm.
	Name string `json:"name"`
	// MOTD is always an empty string.
	MOTD string `json:"motd"`
	// DefaultPermission is the default permission level of the Realm world.
	// one of ["MEMBER", "OPERATOR"]
	DefaultPermission string `json:"defaultPermission"`
	// State is the current state of the realm
	// one of: ["OPEN", "CLOSED"]
	State string `json:"state"`
	// DaysLeft is the days remaining before renewal of the Realm as an integer.
	// (always 0 for Realms where the current user is not the owner)
	DaysLeft int `json:"daysLeft"`
	// Expired is whether the Realm has expired as a trial or not.
	Expired bool `json:"expired"`
	// ExpiredTrial is whether the Realm has expired as a trial or not.
	ExpiredTrial bool `json:"expiredTrial"`
	// GracePeriod is whether the Realm is in its grace period after expiry or not.
	GracePeriod bool `json:"gracePeriod"`
	// WorldType is the world type of the currently loaded world.
	WorldType string `json:"worldType"`
	// Players is a list of the players currently online in the realm
	// NOTE: this is only sent when directly requesting a realm.
	Players []Player `json:"players"`
	// MaxPlayers is how many player slots this realm has.
	MaxPlayers int `json:"maxPlayers"`
	// MinigameName is always null
	MinigameName string `json:"minigameName"`
	// MinigameID is always null
	MinigameID string `json:"minigameId"`
	// MinigameImage is always null
	MinigameImage string `json:"minigameImage"`
	// ActiveSlot is unused, always 1
	ActiveSlot int `json:"activeSlot"`
	// Slots is unused, always null
	Slots []struct{} `json:"slots"`
	// Member is Unknown, always false. (even when member or owner)
	Member bool `json:"member"`
	// ClubID is the ID of the associated Xbox Live club as an integer.
	ClubID int64 `json:"clubId"`
	// SubscriptionRefreshStatus is Unknown, always null.
	SubscriptionRefreshStatus struct{} `json:"subscriptionRefreshStatus"`

	// client is the instance of Client that this belongs to.
	client *Client
}

type APIError struct {
	StatusCode int
	Code       int    `json:"errorCode"`
	Msg        string `json:"errorMsg"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("APIError %d %d %s", e.StatusCode, e.Code, e.Msg)
}

type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTPError %d", e.StatusCode)
}

// Address requests the address and port to connect to this realm from the api,
// will wait for the realm to start if it is currently offline.
func (r *Realm) Address(ctx context.Context) (address string, err error) {
	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()
	for range ticker.C {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		body, err := r.client.Request(ctx, fmt.Sprintf("/worlds/%d/join", r.ID))
		if err != nil {
			if err, ok := err.(*HTTPError); ok {
				if err.StatusCode == 503 {
					continue
				}
			}
			return "", err
		}

		var data struct {
			Address       string `json:"address"`
			PendingUpdate bool   `json:"pendingUpdate"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return "", err
		}
		return data.Address, nil
	}
	panic("unreachable")
}

// OnlinePlayers gets all the players currently on this realm,
// Returns a 403 error if the current user is not the owner of the Realm.
func (r *Realm) OnlinePlayers(ctx context.Context) (players []Player, err error) {
	body, err := r.client.Request(ctx, fmt.Sprintf("/worlds/%d", r.ID))
	if err != nil {
		return nil, err
	}

	var response Realm
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return response.Players, nil
}

// XboxToken returns the xbox token used for the api.
func (c *Client) XboxToken(ctx context.Context) (*auth.XBLToken, error) {
	if c.xblToken != nil {
		return c.xblToken, nil
	}

	t, err := c.tokenSrc.Token()
	if err != nil {
		return nil, err
	}

	c.xblToken, err = auth.RequestXBLToken(ctx, t, c.baseUrl)
	return c.xblToken, err
}

// Request sends an http get request to path with the right headers for the api set.
func (c *Client) Request(ctx context.Context, path string) (body []byte, err error) {
	body, err = c.RequestWithMethod(ctx, path, "GET", nil, "")
	return
}

func (c *Client) RequestWithMethod(ctx context.Context, path string, method string, ReqBody io.Reader, ContentType string) (RespBody []byte, err error) {
	if string(path[0]) == "/" {
		path = path[1:]
	}
	url := fmt.Sprintf("%s%s", c.baseUrl, path)
	req, err := http.NewRequestWithContext(ctx, method, url, ReqBody)
	if err != nil {
		return nil, err
	}

	if ContentType != "" {
		req.Header.Set("Content-Type", ContentType)
	}

	req.Header.Set("User-Agent", "MCPE/UWP")
	req.Header.Set("Client-Version", c.ClientVersion)
	xbl, err := c.XboxToken(ctx)
	if err != nil {
		return nil, err
	}
	xbl.SetAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	RespBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		var apiError APIError
		if _err := json.Unmarshal(RespBody, &apiError); _err != nil {
			return RespBody, &HTTPError{StatusCode: resp.StatusCode}
		}
		apiError.StatusCode = resp.StatusCode

		return RespBody, &apiError
	}

	return RespBody, nil
}

// Realm gets a realm by its invite code.
func (c *Client) Realm(ctx context.Context, code string) (Realm, error) {
	body, err := c.Request(ctx, fmt.Sprintf("/worlds/v1/link/%s", code))
	if err != nil {
		return Realm{}, err
	}

	var realm Realm
	if err := json.Unmarshal(body, &realm); err != nil {
		return Realm{}, err
	}
	realm.client = c

	return realm, nil
}

// Realms gets a list of all realms the token has access to.
func (c *Client) Realms(ctx context.Context) ([]Realm, error) {
	body, err := c.Request(ctx, "/worlds")
	if err != nil {
		return nil, err
	}

	var response struct {
		Servers []Realm `json:"servers"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	realms := response.Servers
	for i := range realms {
		realms[i].client = c
	}

	return realms, nil
}
