package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	baseURL string
	token   string
}

func New(baseURL, token string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token}
}

func (c *Client) Verify(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func (c *Client) wsURL() (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/api/websocket"
	return u.String(), nil
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	wsURL, err := c.wsURL()
	if err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("connecting to Home Assistant WebSocket: %w", err)
	}

	// Expect auth_required
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading auth_required: %w", err)
	}
	if msg["type"] != "auth_required" {
		conn.Close()
		return nil, fmt.Errorf("expected auth_required, got %v", msg["type"])
	}

	// Send auth
	if err := conn.WriteJSON(map[string]string{"type": "auth", "access_token": c.token}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("sending auth: %w", err)
	}

	// Expect auth_ok
	if err := conn.ReadJSON(&msg); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reading auth response: %w", err)
	}
	if msg["type"] == "auth_invalid" {
		conn.Close()
		return nil, fmt.Errorf("invalid token: %v", msg["message"])
	}
	if msg["type"] != "auth_ok" {
		conn.Close()
		return nil, fmt.Errorf("unexpected auth response: %v", msg["type"])
	}

	return conn, nil
}

type Device struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	NameByUser    *string    `json:"name_by_user"`
	Manufacturer  *string    `json:"manufacturer"`
	Model         *string    `json:"model"`
	AreaID        *string    `json:"area_id"`
	ConfigEntries []string   `json:"config_entries"`
	Connections   [][]string `json:"connections"`
	Identifiers   [][]string `json:"identifiers"`
	DisabledBy    *string    `json:"disabled_by"`
	EntryType     *string    `json:"entry_type"`
	HWVersion     *string    `json:"hw_version"`
	SerialNumber  *string    `json:"serial_number"`
	ViaDeviceID   *string    `json:"via_device_id"`
}

type wsResult struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Error   *wsError        `json:"error"`
}

type wsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type EntityState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

var restClient = &http.Client{Timeout: 30 * time.Second}

func (c *Client) restDo(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var bodyReader *strings.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	var req *http.Request
	var err error
	if bodyReader != nil {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return restClient.Do(req)
}

type Automation struct {
	ID           string          `json:"id"`
	Alias        string          `json:"alias"`
	Description  string          `json:"description"`
	Mode         string          `json:"mode"`
	Trigger      json.RawMessage `json:"triggers,omitempty"`
	Condition    json.RawMessage `json:"conditions,omitempty"`
	Action       json.RawMessage `json:"actions,omitempty"`
	UseBlueprint *UseBlueprint   `json:"use_blueprint,omitempty"`
	AreaID       *string         `json:"-"`
	Enabled      *bool           `json:"-"`
	Visible      *bool           `json:"-"`
}

type UseBlueprint struct {
	Path  string          `json:"path"`
	Input json.RawMessage `json:"input,omitempty"`
}

func (c *Client) GetAutomations(ctx context.Context) ([]Automation, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/config/automation/config", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching automations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching automations", resp.StatusCode)
	}

	var automations []Automation
	if err := json.NewDecoder(resp.Body).Decode(&automations); err != nil {
		return nil, fmt.Errorf("decoding automations: %w", err)
	}
	return automations, nil
}

func (c *Client) GetAutomation(ctx context.Context, id string) (*Automation, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/config/automation/config/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching automation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching automation", resp.StatusCode)
	}

	var automation Automation
	if err := json.NewDecoder(resp.Body).Decode(&automation); err != nil {
		return nil, fmt.Errorf("decoding automation: %w", err)
	}

	entry, err := c.GetEntityByUniqueID(ctx, automation.ID)
	if err != nil {
		return nil, fmt.Errorf("fetching automation entity registry: %w", err)
	}
	if entry != nil {
		automation.AreaID = entry.AreaID
		enabled := entry.DisabledBy == nil
		automation.Enabled = &enabled
		visible := entry.HiddenBy == nil
		automation.Visible = &visible
	}
	return &automation, nil
}

func (c *Client) CreateAutomation(ctx context.Context, a Automation) (*Automation, error) {
	if a.ID == "" {
		a.ID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	body, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encoding automation: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/automation/config/"+a.ID, body)
	if err != nil {
		return nil, fmt.Errorf("creating automation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating automation", resp.StatusCode)
	}

	resp.Body.Close()
	if err := c.applyAutomationEntityState(ctx, a.ID, a.AreaID, a.Enabled, a.Visible); err != nil {
		return nil, err
	}
	return c.GetAutomation(ctx, a.ID)
}

func (c *Client) UpdateAutomation(ctx context.Context, a Automation) (*Automation, error) {
	body, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encoding automation: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/automation/config/"+a.ID, body)
	if err != nil {
		return nil, fmt.Errorf("updating automation: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d updating automation", resp.StatusCode)
	}

	if err := c.applyAutomationEntityState(ctx, a.ID, a.AreaID, a.Enabled, a.Visible); err != nil {
		return nil, err
	}
	return c.GetAutomation(ctx, a.ID)
}

func (c *Client) applyAutomationEntityState(ctx context.Context, automationID string, areaID *string, enabled, visible *bool) error {
	return c.applyEntityRegistryState(ctx, automationID, areaID, enabled, visible)
}

// applyEntityRegistryState updates the area, enabled and visible flags on the
// entity registry entry whose unique_id matches uniqueID. Config-based resources
// (automations, scenes, scripts) register an entity keyed by their config id, so
// these registry-only attributes are applied separately from the config itself.
func (c *Client) applyEntityRegistryState(ctx context.Context, uniqueID string, areaID *string, enabled, visible *bool) error {
	entry, err := c.waitForEntityByUniqueID(ctx, uniqueID)
	if err != nil {
		return fmt.Errorf("finding entity: %w", err)
	}
	if entry == nil {
		return nil
	}
	update := EntityUpdate{
		EntityID:  entry.EntityID,
		AreaID:    areaID,
		SetAreaID: true,
	}
	if enabled != nil {
		update.SetDisabledBy = true
		if !*enabled {
			s := "user"
			update.DisabledBy = &s
		}
	}
	if visible != nil {
		update.SetHiddenBy = true
		if !*visible {
			s := "user"
			update.HiddenBy = &s
		}
	}
	_, err = c.UpdateEntity(ctx, update)
	return err
}

func (c *Client) DeleteAutomation(ctx context.Context, id string) error {
	resp, err := c.restDo(ctx, http.MethodDelete, "/api/config/automation/config/"+id, nil)
	if err != nil {
		return fmt.Errorf("deleting automation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status %d deleting automation", resp.StatusCode)
	}
	return nil
}

type Blueprint struct {
	Domain    string
	Path      string
	YAML      string
	SourceURL *string
}

type BlueprintMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Domain      string `json:"domain"`
	SourceURL   string `json:"source_url"`
}

func (c *Client) ListBlueprints(ctx context.Context, domain string) (map[string]BlueprintMetadata, error) {
	result, err := c.blueprintCommand(ctx, map[string]any{
		"type":   "blueprint/list",
		"domain": domain,
	})
	if err != nil {
		return nil, err
	}
	var raw map[string]struct {
		Metadata BlueprintMetadata `json:"metadata"`
		Error    string            `json:"error"`
	}
	if err := json.Unmarshal(result, &raw); err != nil {
		return nil, fmt.Errorf("decoding blueprints: %w", err)
	}
	out := make(map[string]BlueprintMetadata, len(raw))
	for path, v := range raw {
		out[path] = v.Metadata
	}
	return out, nil
}

func (c *Client) GetBlueprint(ctx context.Context, domain, path string) (*BlueprintMetadata, error) {
	blueprints, err := c.ListBlueprints(ctx, domain)
	if err != nil {
		return nil, err
	}
	if meta, ok := blueprints[path]; ok {
		return &meta, nil
	}
	return nil, nil
}

func (c *Client) SaveBlueprint(ctx context.Context, b Blueprint, allowOverride bool) error {
	fields := map[string]any{
		"type":           "blueprint/save",
		"domain":         b.Domain,
		"path":           b.Path,
		"yaml":           b.YAML,
		"allow_override": allowOverride,
	}
	if b.SourceURL != nil {
		fields["source_url"] = *b.SourceURL
	}
	_, err := c.blueprintCommand(ctx, fields)
	return err
}

func (c *Client) DeleteBlueprint(ctx context.Context, domain, path string) error {
	_, err := c.blueprintCommand(ctx, map[string]any{
		"type":   "blueprint/delete",
		"domain": domain,
		"path":   path,
	})
	return err
}

func (c *Client) blueprintCommand(ctx context.Context, fields map[string]any) (json.RawMessage, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	msg := map[string]any{"id": 1}
	for k, v := range fields {
		msg[k] = v
	}
	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("sending blueprint command: %w", err)
	}

	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading blueprint response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("blueprint command error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("blueprint command failed")
	}
	return result.Result, nil
}

type Scene struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Icon     string          `json:"icon,omitempty"`
	Entities json.RawMessage `json:"entities,omitempty"`
	AreaID   *string         `json:"-"`
	Enabled  *bool           `json:"-"`
	Visible  *bool           `json:"-"`
}

func (c *Client) GetScene(ctx context.Context, id string) (*Scene, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/config/scene/config/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching scene: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching scene", resp.StatusCode)
	}

	var scene Scene
	if err := json.NewDecoder(resp.Body).Decode(&scene); err != nil {
		return nil, fmt.Errorf("decoding scene: %w", err)
	}

	entry, err := c.GetEntityByUniqueID(ctx, scene.ID)
	if err != nil {
		return nil, fmt.Errorf("fetching scene entity registry: %w", err)
	}
	if entry != nil {
		scene.AreaID = entry.AreaID
		enabled := entry.DisabledBy == nil
		scene.Enabled = &enabled
		visible := entry.HiddenBy == nil
		scene.Visible = &visible
	}
	return &scene, nil
}

func (c *Client) CreateScene(ctx context.Context, s Scene) (*Scene, error) {
	if s.ID == "" {
		s.ID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	body, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encoding scene: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/scene/config/"+s.ID, body)
	if err != nil {
		return nil, fmt.Errorf("creating scene: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating scene", resp.StatusCode)
	}

	if err := c.applyEntityRegistryState(ctx, s.ID, s.AreaID, s.Enabled, s.Visible); err != nil {
		return nil, err
	}
	return c.GetScene(ctx, s.ID)
}

func (c *Client) UpdateScene(ctx context.Context, s Scene) (*Scene, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encoding scene: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/scene/config/"+s.ID, body)
	if err != nil {
		return nil, fmt.Errorf("updating scene: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d updating scene", resp.StatusCode)
	}

	if err := c.applyEntityRegistryState(ctx, s.ID, s.AreaID, s.Enabled, s.Visible); err != nil {
		return nil, err
	}
	return c.GetScene(ctx, s.ID)
}

func (c *Client) DeleteScene(ctx context.Context, id string) error {
	resp, err := c.restDo(ctx, http.MethodDelete, "/api/config/scene/config/"+id, nil)
	if err != nil {
		return fmt.Errorf("deleting scene: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status %d deleting scene", resp.StatusCode)
	}
	return nil
}

// Script configs are keyed by their object_id, which becomes the entity suffix
// (script.<object_id>). The object_id is carried in the request path rather than
// the body, so it is excluded from JSON marshalling.
type Script struct {
	ObjectID     string          `json:"-"`
	Alias        string          `json:"alias"`
	Description  string          `json:"description,omitempty"`
	Icon         string          `json:"icon,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	Sequence     json.RawMessage `json:"sequence,omitempty"`
	Fields       json.RawMessage `json:"fields,omitempty"`
	UseBlueprint *UseBlueprint   `json:"use_blueprint,omitempty"`
	AreaID       *string         `json:"-"`
	Enabled      *bool           `json:"-"`
	Visible      *bool           `json:"-"`
}

func (c *Client) GetScript(ctx context.Context, objectID string) (*Script, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/config/script/config/"+objectID, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching script", resp.StatusCode)
	}

	var script Script
	if err := json.NewDecoder(resp.Body).Decode(&script); err != nil {
		return nil, fmt.Errorf("decoding script: %w", err)
	}
	script.ObjectID = objectID

	entry, err := c.GetEntityByUniqueID(ctx, objectID)
	if err != nil {
		return nil, fmt.Errorf("fetching script entity registry: %w", err)
	}
	if entry != nil {
		script.AreaID = entry.AreaID
		enabled := entry.DisabledBy == nil
		script.Enabled = &enabled
		visible := entry.HiddenBy == nil
		script.Visible = &visible
	}
	return &script, nil
}

func (c *Client) CreateScript(ctx context.Context, s Script) (*Script, error) {
	if s.ObjectID == "" {
		s.ObjectID = fmt.Sprintf("%d", time.Now().UnixMilli())
	}

	body, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encoding script: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/script/config/"+s.ObjectID, body)
	if err != nil {
		return nil, fmt.Errorf("creating script: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("unexpected status %d creating script", resp.StatusCode)
	}

	if err := c.applyEntityRegistryState(ctx, s.ObjectID, s.AreaID, s.Enabled, s.Visible); err != nil {
		return nil, err
	}
	return c.GetScript(ctx, s.ObjectID)
}

func (c *Client) UpdateScript(ctx context.Context, s Script) (*Script, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encoding script: %w", err)
	}

	resp, err := c.restDo(ctx, http.MethodPost, "/api/config/script/config/"+s.ObjectID, body)
	if err != nil {
		return nil, fmt.Errorf("updating script: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d updating script", resp.StatusCode)
	}

	if err := c.applyEntityRegistryState(ctx, s.ObjectID, s.AreaID, s.Enabled, s.Visible); err != nil {
		return nil, err
	}
	return c.GetScript(ctx, s.ObjectID)
}

func (c *Client) DeleteScript(ctx context.Context, objectID string) error {
	resp, err := c.restDo(ctx, http.MethodDelete, "/api/config/script/config/"+objectID, nil)
	if err != nil {
		return fmt.Errorf("deleting script: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status %d deleting script", resp.StatusCode)
	}
	return nil
}

func (c *Client) GetStates(ctx context.Context) ([]EntityState, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/states", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching states: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching states", resp.StatusCode)
	}

	var states []EntityState
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil, fmt.Errorf("decoding states: %w", err)
	}
	return states, nil
}

// GetState returns one entity's current state, or nil if Home Assistant has none.
func (c *Client) GetState(ctx context.Context, entityID string) (*EntityState, error) {
	resp, err := c.restDo(ctx, http.MethodGet, "/api/states/"+url.PathEscape(entityID), nil)
	if err != nil {
		return nil, fmt.Errorf("fetching state of %s: %w", entityID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching state of %s", resp.StatusCode, entityID)
	}

	var state EntityState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("decoding state of %s: %w", entityID, err)
	}
	return &state, nil
}

func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding %s.%s data: %w", domain, service, err)
	}
	resp, err := c.restDo(ctx, http.MethodPost, "/api/services/"+domain+"/"+service, body)
	if err != nil {
		return fmt.Errorf("calling %s.%s: %w", domain, service, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("calling %s.%s: status %d: %s", domain, service, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// Home Assistant has no list endpoint for scene/script configs (the per-id
// config endpoints exist, but GET on the collection 404s). These data-source
// listings are therefore derived from /api/states, which reflects the loaded
// entities the same way the Home Assistant UI does.

type SceneSummary struct {
	ID       string
	EntityID string
	Name     string
	Entities []string
}

func (c *Client) GetScenes(ctx context.Context) ([]SceneSummary, error) {
	states, err := c.GetStates(ctx)
	if err != nil {
		return nil, err
	}
	var scenes []SceneSummary
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, "scene.") {
			continue
		}
		scenes = append(scenes, SceneSummary{
			ID:       attrString(s.Attributes, "id"),
			EntityID: s.EntityID,
			Name:     attrName(s.Attributes, s.EntityID),
			Entities: attrStringSlice(s.Attributes, "entity_id"),
		})
	}
	return scenes, nil
}

type ScriptSummary struct {
	ID       string
	EntityID string
	Name     string
	Mode     string
	State    string
}

func (c *Client) GetScripts(ctx context.Context) ([]ScriptSummary, error) {
	states, err := c.GetStates(ctx)
	if err != nil {
		return nil, err
	}
	var scripts []ScriptSummary
	for _, s := range states {
		if !strings.HasPrefix(s.EntityID, "script.") {
			continue
		}
		scripts = append(scripts, ScriptSummary{
			ID:       strings.TrimPrefix(s.EntityID, "script."),
			EntityID: s.EntityID,
			Name:     attrName(s.Attributes, s.EntityID),
			Mode:     attrString(s.Attributes, "mode"),
			State:    s.State,
		})
	}
	return scripts, nil
}

func attrString(attrs map[string]any, key string) string {
	if v, ok := attrs[key].(string); ok {
		return v
	}
	return ""
}

func attrName(attrs map[string]any, fallback string) string {
	if name := attrString(attrs, "friendly_name"); name != "" {
		return name
	}
	return fallback
}

func attrStringSlice(attrs map[string]any, key string) []string {
	raw, ok := attrs[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type Area struct {
	AreaID  string   `json:"area_id"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Icon    *string  `json:"icon"`
	Picture *string  `json:"picture"`
	FloorID *string  `json:"floor_id"`
}

func (c *Client) GetAreas(ctx context.Context) ([]Area, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"id": 1, "type": "config/area_registry/list"}); err != nil {
		return nil, fmt.Errorf("sending area_registry/list: %w", err)
	}

	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading area registry response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("area registry error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("area registry request failed")
	}

	var areas []Area
	if err := json.Unmarshal(result.Result, &areas); err != nil {
		return nil, fmt.Errorf("decoding areas: %w", err)
	}
	return areas, nil
}

func (c *Client) GetArea(ctx context.Context, areaID string) (*Area, error) {
	areas, err := c.GetAreas(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range areas {
		if a.AreaID == areaID {
			return &a, nil
		}
	}
	return nil, nil
}

func (c *Client) CreateArea(ctx context.Context, name string, icon, floorID *string) (*Area, error) {
	fields := map[string]any{"name": name, "floor_id": floorID}
	if icon != nil {
		fields["icon"] = *icon
	}
	return c.areaCommand(ctx, "config/area_registry/create", fields)
}

func (c *Client) UpdateArea(ctx context.Context, areaID, name string, icon, floorID *string) (*Area, error) {
	fields := map[string]any{
		"area_id":  areaID,
		"name":     name,
		"floor_id": floorID,
	}
	if icon != nil {
		fields["icon"] = *icon
	}
	return c.areaCommand(ctx, "config/area_registry/update", fields)
}

func (c *Client) DeleteArea(ctx context.Context, areaID string) error {
	_, err := c.areaCommand(ctx, "config/area_registry/delete", map[string]any{
		"area_id": areaID,
	})
	return err
}

type Floor struct {
	FloorID string  `json:"floor_id"`
	Name    string  `json:"name"`
	Level   *int    `json:"level"`
	Icon    *string `json:"icon"`
}

func (c *Client) GetFloors(ctx context.Context) ([]Floor, error) {
	result, err := c.floorCommand(ctx, "config/floor_registry/list", nil)
	if err != nil {
		return nil, err
	}
	var floors []Floor
	if err := json.Unmarshal(result, &floors); err != nil {
		return nil, fmt.Errorf("decoding floors: %w", err)
	}
	return floors, nil
}

func (c *Client) GetFloor(ctx context.Context, floorID string) (*Floor, error) {
	floors, err := c.GetFloors(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range floors {
		if f.FloorID == floorID {
			return &f, nil
		}
	}
	return nil, nil
}

func (c *Client) CreateFloor(ctx context.Context, name string, level *int, icon *string) (*Floor, error) {
	fields := map[string]any{"name": name, "level": level, "icon": icon}
	return c.floorCommandOne(ctx, "config/floor_registry/create", fields)
}

func (c *Client) UpdateFloor(ctx context.Context, floorID, name string, level *int, icon *string) (*Floor, error) {
	fields := map[string]any{"floor_id": floorID, "name": name, "level": level, "icon": icon}
	return c.floorCommandOne(ctx, "config/floor_registry/update", fields)
}

func (c *Client) DeleteFloor(ctx context.Context, floorID string) error {
	_, err := c.floorCommand(ctx, "config/floor_registry/delete", map[string]any{"floor_id": floorID})
	return err
}

func (c *Client) floorCommandOne(ctx context.Context, cmdType string, fields map[string]any) (*Floor, error) {
	result, err := c.floorCommand(ctx, cmdType, fields)
	if err != nil {
		return nil, err
	}
	var floor Floor
	if err := json.Unmarshal(result, &floor); err != nil {
		return nil, fmt.Errorf("decoding floor: %w", err)
	}
	return &floor, nil
}

func (c *Client) floorCommand(ctx context.Context, cmdType string, fields map[string]any) (json.RawMessage, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	msg := map[string]any{"id": 1, "type": cmdType}
	for k, v := range fields {
		msg[k] = v
	}
	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("sending floor command: %w", err)
	}
	var wsResult struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := conn.ReadJSON(&wsResult); err != nil {
		return nil, fmt.Errorf("reading floor response: %w", err)
	}
	if !wsResult.Success {
		if wsResult.Error != nil {
			return nil, fmt.Errorf("floor command failed: %s", wsResult.Error.Message)
		}
		return nil, fmt.Errorf("floor command failed")
	}
	return wsResult.Result, nil
}

func (c *Client) areaCommand(ctx context.Context, cmdType string, fields map[string]any) (*Area, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := map[string]any{"id": 1, "type": cmdType}
	for k, v := range fields {
		payload[k] = v
	}

	if err := conn.WriteJSON(payload); err != nil {
		return nil, fmt.Errorf("sending %s: %w", cmdType, err)
	}

	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading %s response: %w", cmdType, err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("%s error %s: %s", cmdType, result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("%s failed", cmdType)
	}

	if len(result.Result) == 0 || result.Result[0] != '{' {
		return nil, nil
	}
	var area Area
	if err := json.Unmarshal(result.Result, &area); err != nil {
		return nil, fmt.Errorf("decoding area response: %w", err)
	}
	return &area, nil
}

type EntityRegistryEntry struct {
	ID           string   `json:"id"`
	UniqueID     string   `json:"unique_id"`
	EntityID     string   `json:"entity_id"`
	DeviceID     *string  `json:"device_id"`
	Name         *string  `json:"name"`
	OriginalName *string  `json:"original_name"`
	Platform     string   `json:"platform"`
	AreaID       *string  `json:"area_id"`
	DisabledBy   *string  `json:"disabled_by"`
	HiddenBy     *string  `json:"hidden_by"`
	Icon         *string  `json:"icon"`
	Aliases      []string `json:"aliases"`
	Labels       []string `json:"labels"`
}

func (c *Client) GetEntityRegistry(ctx context.Context) ([]EntityRegistryEntry, error) {
	_, entries, err := c.getRegistries(ctx)
	return entries, err
}

func (c *Client) GetEntityByUniqueID(ctx context.Context, uniqueID string) (*EntityRegistryEntry, error) {
	entries, err := c.GetEntityRegistry(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.UniqueID == uniqueID {
			return &e, nil
		}
	}
	return nil, nil
}

// waitForEntityByUniqueID polls the entity registry for the entry whose
// unique_id matches uniqueID. Config-based resources (automations, scenes,
// scripts) register their entity asynchronously after the config is written,
// so immediately after a create the entry may not exist yet. Without this
// wait, registry-only attributes (area_id, enabled, visible) would be applied
// to a missing entry and silently dropped, causing a create/read mismatch.
func (c *Client) waitForEntityByUniqueID(ctx context.Context, uniqueID string) (*EntityRegistryEntry, error) {
	const attempts = 20
	for i := 0; i < attempts; i++ {
		entry, err := c.GetEntityByUniqueID(ctx, uniqueID)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			return entry, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, nil
}

func (c *Client) GetEntityRegistryEntry(ctx context.Context, entityID string) (*EntityRegistryEntry, error) {
	entries, err := c.GetEntityRegistry(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.EntityID == entityID {
			return &e, nil
		}
	}
	return nil, nil
}

type EntityUpdate struct {
	EntityID      string
	Name          *string
	Icon          *string
	AreaID        *string
	DisabledBy    *string
	HiddenBy      *string
	SetName       bool
	SetIcon       bool
	SetAreaID     bool
	SetDisabledBy bool
	SetHiddenBy   bool
	SetAliases    bool
	SetLabels     bool
	Aliases       []string
	Labels        []string
}

func (c *Client) UpdateEntity(ctx context.Context, update EntityUpdate) (*EntityRegistryEntry, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := map[string]any{
		"id":        1,
		"type":      "config/entity_registry/update",
		"entity_id": update.EntityID,
	}
	if update.SetName {
		payload["name"] = update.Name
	}
	if update.SetIcon {
		payload["icon"] = update.Icon
	}
	if update.SetAreaID {
		payload["area_id"] = update.AreaID
	}
	if update.SetDisabledBy {
		payload["disabled_by"] = update.DisabledBy
	}
	if update.SetHiddenBy {
		payload["hidden_by"] = update.HiddenBy
	}
	if update.SetAliases {
		if update.Aliases == nil {
			update.Aliases = []string{}
		}
		payload["aliases"] = update.Aliases
	}
	if update.SetLabels {
		if update.Labels == nil {
			update.Labels = []string{}
		}
		payload["labels"] = update.Labels
	}

	if err := conn.WriteJSON(payload); err != nil {
		return nil, fmt.Errorf("sending entity_registry/update: %w", err)
	}

	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading entity update response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("entity update error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("entity update failed")
	}

	// HA wraps the entity entry in an "entity_entry" key for this command
	var wrapper struct {
		EntityEntry EntityRegistryEntry `json:"entity_entry"`
	}
	if err := json.Unmarshal(result.Result, &wrapper); err != nil {
		return nil, fmt.Errorf("decoding entity update response: %w", err)
	}
	return &wrapper.EntityEntry, nil
}

func (c *Client) GetDevices(ctx context.Context) ([]Device, error) {
	devices, _, err := c.getRegistries(ctx)
	return devices, err
}

func (c *Client) GetDevice(ctx context.Context, id string) (*Device, error) {
	devices, err := c.GetDevices(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if d.ID == id {
			return &d, nil
		}
	}
	return nil, nil
}

func (c *Client) GetEntitiesForDevice(ctx context.Context, deviceID string) ([]EntityRegistryEntry, error) {
	_, entries, err := c.getRegistries(ctx)
	if err != nil {
		return nil, err
	}
	var result []EntityRegistryEntry
	for _, e := range entries {
		if e.DeviceID != nil && *e.DeviceID == deviceID {
			result = append(result, e)
		}
	}
	return result, nil
}

type DeviceUpdate struct {
	DeviceID   string  `json:"device_id"`
	NameByUser *string `json:"name_by_user"`
	AreaID     *string `json:"area_id"`
	DisabledBy *string `json:"disabled_by"`
}

func (c *Client) UpdateDevice(ctx context.Context, update DeviceUpdate) (*Device, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	payload := map[string]any{
		"id":           1,
		"type":         "config/device_registry/update",
		"device_id":    update.DeviceID,
		"name_by_user": update.NameByUser,
		"area_id":      update.AreaID,
		"disabled_by":  update.DisabledBy,
	}

	if err := conn.WriteJSON(payload); err != nil {
		return nil, fmt.Errorf("sending device_registry/update: %w", err)
	}

	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading update response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("device update error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("device update failed")
	}

	var device Device
	if err := json.Unmarshal(result.Result, &device); err != nil {
		return nil, fmt.Errorf("decoding updated device: %w", err)
	}
	return &device, nil
}

func (c *Client) GetDevicesAndEntities(ctx context.Context) ([]Device, []EntityRegistryEntry, error) {
	return c.getRegistries(ctx)
}

func (c *Client) getRegistries(ctx context.Context) ([]Device, []EntityRegistryEntry, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()

	// Send both commands before reading to pipeline the requests.
	if err := conn.WriteJSON(map[string]any{"id": 1, "type": "config/device_registry/list"}); err != nil {
		return nil, nil, fmt.Errorf("sending device_registry/list: %w", err)
	}
	if err := conn.WriteJSON(map[string]any{"id": 2, "type": "config/entity_registry/list"}); err != nil {
		return nil, nil, fmt.Errorf("sending entity_registry/list: %w", err)
	}

	results := make(map[int]json.RawMessage)
	for i := 0; i < 2; i++ {
		var r wsResult
		if err := conn.ReadJSON(&r); err != nil {
			return nil, nil, fmt.Errorf("reading registry response: %w", err)
		}
		if !r.Success {
			if r.Error != nil {
				return nil, nil, fmt.Errorf("registry request %d error %s: %s", r.ID, r.Error.Code, r.Error.Message)
			}
			return nil, nil, fmt.Errorf("registry request %d failed", r.ID)
		}
		results[r.ID] = r.Result
	}

	var devices []Device
	if err := json.Unmarshal(results[1], &devices); err != nil {
		return nil, nil, fmt.Errorf("decoding devices: %w", err)
	}

	var entities []EntityRegistryEntry
	if err := json.Unmarshal(results[2], &entities); err != nil {
		return nil, nil, fmt.Errorf("decoding entity registry: %w", err)
	}

	return devices, entities, nil
}

// SetEntityExposure exposes or unexposes an entity for the given assistants.
func (c *Client) SetEntityExposure(ctx context.Context, entityID string, assistants []string, shouldExpose bool) error {
	if len(assistants) == 0 {
		return nil
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	payload := map[string]any{
		"id":            1,
		"type":          "homeassistant/expose_entity",
		"assistants":    assistants,
		"entity_ids":    []string{entityID},
		"should_expose": shouldExpose,
	}
	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("sending expose_entity: %w", err)
	}
	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return fmt.Errorf("reading expose_entity response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("expose_entity error %s: %s", result.Error.Code, result.Error.Message)
		}
		return fmt.Errorf("expose_entity failed")
	}
	return nil
}

// GetExposedAssistants returns the assistants an entity is currently exposed to.
func (c *Client) GetExposedAssistants(ctx context.Context, entityID string) ([]string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"id": 1, "type": "homeassistant/expose_entity/list"}); err != nil {
		return nil, fmt.Errorf("sending expose_entity/list: %w", err)
	}
	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading expose_entity/list response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("expose_entity/list error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("expose_entity/list failed")
	}
	var wrapper struct {
		ExposedEntities map[string]map[string]bool `json:"exposed_entities"`
	}
	if err := json.Unmarshal(result.Result, &wrapper); err != nil {
		return nil, fmt.Errorf("decoding expose_entity/list: %w", err)
	}
	var assistants []string
	for a, exposed := range wrapper.ExposedEntities[entityID] {
		if exposed {
			assistants = append(assistants, a)
		}
	}
	return assistants, nil
}

// Label is a Home Assistant label registry entry.
type Label struct {
	LabelID     string  `json:"label_id"`
	Name        string  `json:"name"`
	Color       *string `json:"color"`
	Icon        *string `json:"icon"`
	Description *string `json:"description"`
}

func (c *Client) CreateLabel(ctx context.Context, name string, color, icon, description *string) (*Label, error) {
	fields := map[string]any{"name": name, "color": color, "icon": icon, "description": description}
	return c.labelCommandOne(ctx, "config/label_registry/create", fields)
}

func (c *Client) UpdateLabel(ctx context.Context, labelID, name string, color, icon, description *string) (*Label, error) {
	fields := map[string]any{"label_id": labelID, "name": name, "color": color, "icon": icon, "description": description}
	return c.labelCommandOne(ctx, "config/label_registry/update", fields)
}

func (c *Client) DeleteLabel(ctx context.Context, labelID string) error {
	_, err := c.labelCommand(ctx, "config/label_registry/delete", map[string]any{"label_id": labelID})
	return err
}

func (c *Client) GetLabels(ctx context.Context) ([]Label, error) {
	result, err := c.labelCommand(ctx, "config/label_registry/list", nil)
	if err != nil {
		return nil, err
	}
	var labels []Label
	if err := json.Unmarshal(result, &labels); err != nil {
		return nil, fmt.Errorf("decoding labels: %w", err)
	}
	return labels, nil
}

func (c *Client) GetLabel(ctx context.Context, labelID string) (*Label, error) {
	labels, err := c.GetLabels(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if l.LabelID == labelID {
			return &l, nil
		}
	}
	return nil, nil
}

func (c *Client) labelCommandOne(ctx context.Context, cmdType string, fields map[string]any) (*Label, error) {
	result, err := c.labelCommand(ctx, cmdType, fields)
	if err != nil {
		return nil, err
	}
	var label Label
	if err := json.Unmarshal(result, &label); err != nil {
		return nil, fmt.Errorf("decoding label: %w", err)
	}
	return &label, nil
}

func (c *Client) labelCommand(ctx context.Context, cmdType string, fields map[string]any) (json.RawMessage, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	msg := map[string]any{"id": 1, "type": cmdType}
	for k, v := range fields {
		msg[k] = v
	}
	if err := conn.WriteJSON(msg); err != nil {
		return nil, fmt.Errorf("sending label command: %w", err)
	}
	var result wsResult
	if err := conn.ReadJSON(&result); err != nil {
		return nil, fmt.Errorf("reading label response: %w", err)
	}
	if !result.Success {
		if result.Error != nil {
			return nil, fmt.Errorf("label command error %s: %s", result.Error.Code, result.Error.Message)
		}
		return nil, fmt.Errorf("label command failed")
	}
	return result.Result, nil
}
