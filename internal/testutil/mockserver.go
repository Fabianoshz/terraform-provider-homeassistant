package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

type MockDevice struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	NameByUser   *string    `json:"name_by_user"`
	Manufacturer *string    `json:"manufacturer"`
	Model        *string    `json:"model"`
	AreaID       *string    `json:"area_id"`
	Connections  [][]string `json:"connections"`
	Identifiers  [][]string `json:"identifiers"`
	DisabledBy   *string    `json:"disabled_by"`
	HWVersion    *string    `json:"hw_version"`
	SerialNumber *string    `json:"serial_number"`
}

type MockEntityRegistry struct {
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

type MockEntityState struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged string         `json:"last_changed"`
	LastUpdated string         `json:"last_updated"`
}

type MockArea struct {
	AreaID  string  `json:"area_id"`
	Name    string  `json:"name"`
	Icon    *string `json:"icon"`
	Picture *string `json:"picture"`
	FloorID *string `json:"floor_id"`
}

type MockAutomation struct {
	ID           string          `json:"id"`
	Alias        string          `json:"alias"`
	Description  string          `json:"description"`
	Mode         string          `json:"mode"`
	Trigger      json.RawMessage `json:"triggers,omitempty"`
	Condition    json.RawMessage `json:"conditions,omitempty"`
	Action       json.RawMessage `json:"actions,omitempty"`
	UseBlueprint json.RawMessage `json:"use_blueprint,omitempty"`
}

type MockScene struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Icon     string          `json:"icon,omitempty"`
	Entities json.RawMessage `json:"entities,omitempty"`
}

type MockScript struct {
	ObjectID     string          `json:"-"`
	Alias        string          `json:"alias"`
	Description  string          `json:"description,omitempty"`
	Icon         string          `json:"icon,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	Sequence     json.RawMessage `json:"sequence,omitempty"`
	Fields       json.RawMessage `json:"fields,omitempty"`
	UseBlueprint json.RawMessage `json:"use_blueprint,omitempty"`
}

type MockFloor struct {
	FloorID string  `json:"floor_id"`
	Name    string  `json:"name"`
	Level   *int    `json:"level"`
	Icon    *string `json:"icon"`
}

type MockBlueprint struct {
	Domain    string
	Path      string
	YAML      string
	SourceURL *string
}

type MockServerConfig struct {
	Token          string
	Areas          []MockArea
	Automations    []MockAutomation
	Blueprints     []MockBlueprint
	Devices        []MockDevice
	EntityRegistry []MockEntityRegistry
	EntityStates   []MockEntityState
	Floors         []MockFloor
	Scenes         []MockScene
	Scripts        []MockScript
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func NewMockHAServer(t *testing.T, cfg MockServerConfig) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// exposed_entities registry (entity_id -> assistant -> exposed), shared
	// across websocket connections since the client dials a fresh one per call.
	exposures := map[string]map[string]bool{}

	mux.HandleFunc("/api/websocket", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("ws upgrade: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteJSON(map[string]string{"type": "auth_required"}); err != nil {
			return
		}

		var authMsg map[string]string
		if err := conn.ReadJSON(&authMsg); err != nil {
			return
		}
		if authMsg["access_token"] != cfg.Token {
			conn.WriteJSON(map[string]any{"type": "auth_invalid", "message": "invalid token"})
			return
		}
		if err := conn.WriteJSON(map[string]string{"type": "auth_ok"}); err != nil {
			return
		}

		for {
			var cmd map[string]any
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			id := int(cmd["id"].(float64))

			var payload any
			switch cmd["type"] {
			case "config/area_registry/list":
				payload = cfg.Areas
			case "config/area_registry/create":
				name, _ := cmd["name"].(string)
				areaID := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
				area := MockArea{AreaID: areaID, Name: name}
				if icon, ok := cmd["icon"].(string); ok {
					area.Icon = Ptr(icon)
				}
				if floorID, ok := cmd["floor_id"].(string); ok {
					area.FloorID = Ptr(floorID)
				}
				cfg.Areas = append(cfg.Areas, area)
				payload = area
			case "config/area_registry/update":
				areaID, _ := cmd["area_id"].(string)
				for i, a := range cfg.Areas {
					if a.AreaID != areaID {
						continue
					}
					if name, ok := cmd["name"].(string); ok {
						cfg.Areas[i].Name = name
					}
					if v, ok := cmd["icon"]; ok {
						if v == nil {
							cfg.Areas[i].Icon = nil
						} else if s, ok := v.(string); ok {
							cfg.Areas[i].Icon = Ptr(s)
						}
					}
					if v, ok := cmd["floor_id"]; ok {
						if v == nil {
							cfg.Areas[i].FloorID = nil
						} else if s, ok := v.(string); ok {
							cfg.Areas[i].FloorID = Ptr(s)
						}
					}
					payload = cfg.Areas[i]
					break
				}
			case "config/area_registry/delete":
				areaID, _ := cmd["area_id"].(string)
				for i, a := range cfg.Areas {
					if a.AreaID == areaID {
						cfg.Areas = append(cfg.Areas[:i], cfg.Areas[i+1:]...)
						break
					}
				}
				payload = map[string]any{}
			case "config/floor_registry/list":
				payload = cfg.Floors
			case "config/floor_registry/create":
				name, _ := cmd["name"].(string)
				floorID := strings.ToLower(strings.ReplaceAll(name, " ", "_"))
				floor := MockFloor{FloorID: floorID, Name: name}
				if icon, ok := cmd["icon"].(string); ok {
					floor.Icon = Ptr(icon)
				}
				if level, ok := cmd["level"].(float64); ok {
					l := int(level)
					floor.Level = &l
				}
				cfg.Floors = append(cfg.Floors, floor)
				payload = floor
			case "config/floor_registry/update":
				floorID, _ := cmd["floor_id"].(string)
				for i, f := range cfg.Floors {
					if f.FloorID != floorID {
						continue
					}
					if name, ok := cmd["name"].(string); ok {
						cfg.Floors[i].Name = name
					}
					if v, ok := cmd["icon"]; ok {
						if v == nil {
							cfg.Floors[i].Icon = nil
						} else if s, ok := v.(string); ok {
							cfg.Floors[i].Icon = Ptr(s)
						}
					}
					if v, ok := cmd["level"]; ok {
						if v == nil {
							cfg.Floors[i].Level = nil
						} else if l, ok := v.(float64); ok {
							li := int(l)
							cfg.Floors[i].Level = &li
						}
					}
					payload = cfg.Floors[i]
					break
				}
			case "config/floor_registry/delete":
				floorID, _ := cmd["floor_id"].(string)
				for i, f := range cfg.Floors {
					if f.FloorID == floorID {
						cfg.Floors = append(cfg.Floors[:i], cfg.Floors[i+1:]...)
						break
					}
				}
				payload = map[string]any{}
			case "blueprint/list":
				domain, _ := cmd["domain"].(string)
				result := map[string]any{}
				for _, b := range cfg.Blueprints {
					if b.Domain == domain {
						result[b.Path] = map[string]any{
							"metadata": map[string]any{"name": b.Path, "domain": domain},
						}
					}
				}
				payload = result
			case "blueprint/save":
				domain, _ := cmd["domain"].(string)
				path, _ := cmd["path"].(string)
				yamlStr, _ := cmd["yaml"].(string)
				bp := MockBlueprint{Domain: domain, Path: path, YAML: yamlStr}
				if s, ok := cmd["source_url"].(string); ok {
					bp.SourceURL = Ptr(s)
				}
				overrides := false
				for i, b := range cfg.Blueprints {
					if b.Domain == domain && b.Path == path {
						cfg.Blueprints[i] = bp
						overrides = true
						break
					}
				}
				if !overrides {
					cfg.Blueprints = append(cfg.Blueprints, bp)
				}
				payload = map[string]any{"overrides_existing": overrides}
			case "blueprint/delete":
				domain, _ := cmd["domain"].(string)
				path, _ := cmd["path"].(string)
				for i, b := range cfg.Blueprints {
					if b.Domain == domain && b.Path == path {
						cfg.Blueprints = append(cfg.Blueprints[:i], cfg.Blueprints[i+1:]...)
						break
					}
				}
				payload = map[string]any{}
			case "config/device_registry/list":
				payload = cfg.Devices
			case "config/entity_registry/list":
				// Like HA, the list omits aliases; only get returns them.
				var list []map[string]any
				raw, _ := json.Marshal(cfg.EntityRegistry)
				json.Unmarshal(raw, &list)
				for _, e := range list {
					delete(e, "aliases")
				}
				payload = list
			case "config/entity_registry/get":
				entityID, _ := cmd["entity_id"].(string)
				found := false
				for _, e := range cfg.EntityRegistry {
					if e.EntityID == entityID {
						payload = e
						found = true
						break
					}
				}
				if !found {
					conn.WriteJSON(map[string]any{
						"id": id, "type": "result", "success": false,
						"error": map[string]any{"code": "not_found", "message": "Entity not found"},
					})
					continue
				}
			case "config/entity_registry/update":
				entityID, _ := cmd["entity_id"].(string)
				for i, e := range cfg.EntityRegistry {
					if e.EntityID != entityID {
						continue
					}
					if v, ok := cmd["name"]; ok {
						if v == nil {
							cfg.EntityRegistry[i].Name = nil
						} else if s, ok := v.(string); ok {
							cfg.EntityRegistry[i].Name = Ptr(s)
						}
					}
					if v, ok := cmd["icon"]; ok {
						if v == nil {
							cfg.EntityRegistry[i].Icon = nil
						} else if s, ok := v.(string); ok {
							cfg.EntityRegistry[i].Icon = Ptr(s)
						}
					}
					if v, ok := cmd["area_id"]; ok {
						if v == nil {
							cfg.EntityRegistry[i].AreaID = nil
						} else if s, ok := v.(string); ok {
							cfg.EntityRegistry[i].AreaID = Ptr(s)
						}
					}
					if v, ok := cmd["disabled_by"]; ok {
						if v == nil {
							cfg.EntityRegistry[i].DisabledBy = nil
						} else if s, ok := v.(string); ok {
							cfg.EntityRegistry[i].DisabledBy = Ptr(s)
						}
					}
					if v, ok := cmd["hidden_by"]; ok {
						if v == nil {
							cfg.EntityRegistry[i].HiddenBy = nil
						} else if s, ok := v.(string); ok {
							cfg.EntityRegistry[i].HiddenBy = Ptr(s)
						}
					}
					if v, ok := cmd["aliases"]; ok {
						cfg.EntityRegistry[i].Aliases = toStrings(v)
					}
					if v, ok := cmd["labels"]; ok {
						cfg.EntityRegistry[i].Labels = toStrings(v)
					}
					// HA wraps the result in entity_entry for this command
					result, _ := json.Marshal(map[string]any{"entity_entry": cfg.EntityRegistry[i]})
					conn.WriteJSON(map[string]any{
						"id": id, "type": "result", "success": true,
						"result": json.RawMessage(result),
					})
					payload = nil // already sent
					break
				}
				if payload == nil {
					continue
				}
			case "config/device_registry/update":
				deviceID, _ := cmd["device_id"].(string)
				for i, d := range cfg.Devices {
					if d.ID != deviceID {
						continue
					}
					if v, ok := cmd["name_by_user"]; ok {
						if v == nil {
							cfg.Devices[i].NameByUser = nil
						} else if s, ok := v.(string); ok {
							cfg.Devices[i].NameByUser = Ptr(s)
						}
					}
					if v, ok := cmd["area_id"]; ok {
						if v == nil {
							cfg.Devices[i].AreaID = nil
						} else if s, ok := v.(string); ok {
							cfg.Devices[i].AreaID = Ptr(s)
						}
					}
					if v, ok := cmd["disabled_by"]; ok {
						if v == nil {
							cfg.Devices[i].DisabledBy = nil
						} else if s, ok := v.(string); ok {
							cfg.Devices[i].DisabledBy = Ptr(s)
						}
					}
					payload = cfg.Devices[i]
					break
				}
			case "homeassistant/expose_entity":
				shouldExpose, _ := cmd["should_expose"].(bool)
				collect := func(key string) []string {
					var out []string
					if arr, ok := cmd[key].([]any); ok {
						for _, v := range arr {
							if s, ok := v.(string); ok {
								out = append(out, s)
							}
						}
					}
					return out
				}
				for _, e := range collect("entity_ids") {
					if exposures[e] == nil {
						exposures[e] = map[string]bool{}
					}
					for _, a := range collect("assistants") {
						exposures[e][a] = shouldExpose
					}
				}
				payload = map[string]any{}
			case "homeassistant/expose_entity/list":
				payload = map[string]any{"exposed_entities": exposures}
			default:
				continue
			}

			result, _ := json.Marshal(payload)
			conn.WriteJSON(map[string]any{
				"id":      id,
				"type":    "result",
				"success": true,
				"result":  json.RawMessage(result),
			})
		}
	})

	mux.HandleFunc("/api/config/automation/config/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/config/automation/config/")
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			for _, a := range cfg.Automations {
				if a.ID == id {
					json.NewEncoder(w).Encode(a)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			var a MockAutomation
			json.NewDecoder(r.Body).Decode(&a)
			a.ID = id
			for i, existing := range cfg.Automations {
				if existing.ID == id {
					cfg.Automations[i] = a
					json.NewEncoder(w).Encode(a)
					return
				}
			}
			cfg.Automations = append(cfg.Automations, a)
			json.NewEncoder(w).Encode(a)
		case http.MethodDelete:
			for i, a := range cfg.Automations {
				if a.ID == id {
					cfg.Automations = append(cfg.Automations[:i], cfg.Automations[i+1:]...)
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})

	mux.HandleFunc("/api/config/automation/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(cfg.Automations)
		case http.MethodPost:
			var a MockAutomation
			json.NewDecoder(r.Body).Decode(&a)
			a.ID = fmt.Sprintf("automation_%d", len(cfg.Automations)+1)
			cfg.Automations = append(cfg.Automations, a)
			json.NewEncoder(w).Encode(a)
		}
	})

	mux.HandleFunc("/api/config/scene/config/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/config/scene/config/")
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			for _, s := range cfg.Scenes {
				if s.ID == id {
					json.NewEncoder(w).Encode(s)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			var s MockScene
			json.NewDecoder(r.Body).Decode(&s)
			s.ID = id
			for i, existing := range cfg.Scenes {
				if existing.ID == id {
					cfg.Scenes[i] = s
					json.NewEncoder(w).Encode(s)
					return
				}
			}
			cfg.Scenes = append(cfg.Scenes, s)
			json.NewEncoder(w).Encode(s)
		case http.MethodDelete:
			for i, s := range cfg.Scenes {
				if s.ID == id {
					cfg.Scenes = append(cfg.Scenes[:i], cfg.Scenes[i+1:]...)
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})

	mux.HandleFunc("/api/config/script/config/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		objectID := strings.TrimPrefix(r.URL.Path, "/api/config/script/config/")
		w.Header().Set("Content-Type", "application/json")

		switch r.Method {
		case http.MethodGet:
			for _, s := range cfg.Scripts {
				if s.ObjectID == objectID {
					json.NewEncoder(w).Encode(s)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			var s MockScript
			json.NewDecoder(r.Body).Decode(&s)
			s.ObjectID = objectID
			for i, existing := range cfg.Scripts {
				if existing.ObjectID == objectID {
					cfg.Scripts[i] = s
					json.NewEncoder(w).Encode(s)
					return
				}
			}
			cfg.Scripts = append(cfg.Scripts, s)
			json.NewEncoder(w).Encode(s)
		case http.MethodDelete:
			for i, s := range cfg.Scripts {
				if s.ObjectID == objectID {
					cfg.Scripts = append(cfg.Scripts[:i], cfg.Scripts[i+1:]...)
					w.WriteHeader(http.StatusOK)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// Entity states, mutable through the set_value/select_option services.
	var statesMu sync.Mutex
	states := append([]MockEntityState(nil), cfg.EntityStates...)

	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		statesMu.Lock()
		defer statesMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(states)
	})

	mux.HandleFunc("/api/states/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		statesMu.Lock()
		defer statesMu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/api/states/")
		for _, st := range states {
			if st.EntityID == id {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(st)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var data map[string]any
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var value any
		switch strings.TrimPrefix(r.URL.Path, "/api/services/") {
		case "number/set_value", "input_number/set_value", "text/set_value", "input_text/set_value":
			value = data["value"]
		case "select/select_option", "input_select/select_option":
			value = data["option"]
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		statesMu.Lock()
		defer statesMu.Unlock()
		for i := range states {
			if states[i].EntityID == data["entity_id"] {
				states[i].State = fmt.Sprint(value)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode([]MockEntityState{states[i]})
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func Ptr(s string) *string { return &s }

func toStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
