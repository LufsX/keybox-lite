package targets

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"keyboxlite/internal/backend"
)

// App-list locations for the backends that keep the list inside their daemon
// configuration instead of Tricky Store's target.txt / config.ini.
const (
	teesimulatorConfigPath  = "/data/adb/teesim/config.json"
	ohMyKeymintInjectorPath = "/data/misc/keystore/omk/injector.toml"
)

// readTeesimulatorDocument reads the apps of the default profile from
// TEESimulator's config.json. The rest of the file is preserved on write.
func readTeesimulatorDocument(d *disk) (document, error) {
	doc := document{config: emptyConfig(), backend: backend.TeeSimulator}
	s, err := d.read(teesimulatorConfigPath, false)
	if err != nil {
		return doc, err
	}
	if !s.Exists {
		return doc, fmt.Errorf("TEESimulator config.json not found")
	}
	doc.target = s
	profiles, err := teesimulatorProfiles(s.Data)
	if err != nil {
		return doc, err
	}
	apps, ok := profiles["default"]
	if !ok {
		return doc, fmt.Errorf("TEESimulator config.json has no default profile")
	}
	for _, name := range apps {
		doc.config.Targets = append(doc.config.Targets, Target{name, "auto"})
	}
	if err = validateConfig(&doc.config); err != nil {
		return doc, err
	}
	if err = teesimulatorProfileConflict(profiles, doc.config.Targets); err != nil {
		return doc, err
	}
	return doc, nil
}

// teesimulatorProfiles returns each profile's apps. TEESimulator requires exactly
// one owning profile per package, so the manager refuses lists it could make
// ambiguous.
func teesimulatorProfiles(data []byte) (map[string][]string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("invalid TEESimulator config.json")
	}
	profilesRaw, ok := root["profiles"]
	if !ok {
		return nil, fmt.Errorf("TEESimulator config.json has no profiles")
	}
	var rawProfiles map[string]json.RawMessage
	if err := json.Unmarshal(profilesRaw, &rawProfiles); err != nil {
		return nil, fmt.Errorf("invalid TEESimulator profiles")
	}
	out := map[string][]string{}
	for id, raw := range rawProfiles {
		var profile map[string]json.RawMessage
		if err := json.Unmarshal(raw, &profile); err != nil || profile == nil {
			return nil, fmt.Errorf("invalid TEESimulator profile %q", id)
		}
		apps := []string{}
		if value, ok := profile["apps"]; ok {
			if err := json.Unmarshal(value, &apps); err != nil {
				return nil, fmt.Errorf("invalid TEESimulator apps list in profile %q", id)
			}
		}
		out[id] = apps
	}
	return out, nil
}

func teesimulatorProfileConflict(profiles map[string][]string, targets []Target) error {
	owners := map[string]string{}
	for id, apps := range profiles {
		for _, app := range apps {
			if prev, ok := owners[app]; ok && prev != id {
				return fmt.Errorf("TEESimulator app %s appears in profiles %q and %q", app, prev, id)
			}
			owners[app] = id
		}
	}
	for _, t := range targets {
		if owner, ok := owners[t.PackageName]; ok && owner != "default" {
			return fmt.Errorf("TEESimulator app %s is already in profile %q", t.PackageName, owner)
		}
	}
	return nil
}

// validate refuses a save that would make default.apps ambiguous with another
// TEESimulator profile. Tricky Store and OhMyKeymint keep a single app list.
func (doc document) validate(c Config) error {
	if doc.backend != backend.TeeSimulator {
		return nil
	}
	profiles, err := teesimulatorProfiles(doc.target.Data)
	if err != nil {
		return err
	}
	return teesimulatorProfileConflict(profiles, c.Targets)
}

// teesimulatorChanges rewrites only default.apps; every other key of config.json
// is carried through as a raw value.
func (doc document) teesimulatorChanges(c Config) []change {
	out := append(teesimulatorRewrite(doc.target.Data, c.Targets), '\n')
	return []change{{teesimulatorConfigPath, doc.target, fileImage(out, doc.target, false)}}
}

func teesimulatorRewrite(original []byte, targets []Target) []byte {
	var root map[string]json.RawMessage
	json.Unmarshal(original, &root)
	var profiles map[string]json.RawMessage
	json.Unmarshal(root["profiles"], &profiles)
	var def map[string]json.RawMessage
	json.Unmarshal(profiles["default"], &def)
	apps := make([]string, 0, len(targets))
	for _, t := range targets {
		apps = append(apps, t.PackageName)
	}
	appsRaw, _ := json.Marshal(apps)
	def["apps"] = appsRaw
	defRaw, _ := json.Marshal(def)
	profiles["default"] = defRaw
	profilesRaw, _ := json.Marshal(profiles)
	root["profiles"] = profilesRaw
	out, _ := json.Marshal(root)
	return out
}

// readOhMyKeymintDocument reads the scoop array from OhMyKeymint's injector.toml.
// The rest of the file, including the [main]/[filter]/[intercept] sections, is
// preserved on write.
func readOhMyKeymintDocument(d *disk) (document, error) {
	doc := document{config: emptyConfig(), backend: backend.OhMyKeymint}
	s, err := d.read(ohMyKeymintInjectorPath, false)
	if err != nil {
		return doc, err
	}
	if !s.Exists {
		return doc, fmt.Errorf("OhMyKeymint injector.toml not found")
	}
	doc.target = s
	apps, _, _, err := ohMyKeymintScoop(s.Data)
	if err != nil {
		return doc, err
	}
	for _, name := range apps {
		doc.config.Targets = append(doc.config.Targets, Target{name, "auto"})
	}
	if err = validateConfig(&doc.config); err != nil {
		return doc, err
	}
	return doc, nil
}

func (doc document) ohMyKeymintChanges(c Config) []change {
	_, start, end, err := ohMyKeymintScoop(doc.target.Data)
	if err != nil {
		return nil
	}
	var b strings.Builder
	b.Write(doc.target.Data[:start])
	b.WriteString(formatTomlArray(c.Targets))
	b.Write(doc.target.Data[end:])
	out := []byte(b.String())
	return []change{{ohMyKeymintInjectorPath, doc.target, fileImage(out, doc.target, false)}}
}

func formatTomlArray(targets []Target) string {
	if len(targets) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteString("[\n")
	for _, t := range targets {
		b.WriteString("  ")
		b.WriteString(strconv.Quote(t.PackageName))
		b.WriteString(",\n")
	}
	b.WriteString("]")
	return b.String()
}

// ohMyKeymintScoop locates the top-level scoop array and returns its entries and
// the byte range its brackets occupy, so a rewrite preserves the surrounding file.
func ohMyKeymintScoop(data []byte) ([]string, int, int, error) {
	text := string(data)
	offset := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			break
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if key, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "scoop" {
				eq := strings.IndexByte(line, '=')
				start := offset + eq + 1
				for start < len(text) && (text[start] == ' ' || text[start] == '\t' || text[start] == '\n' || text[start] == '\r') {
					start++
				}
				if start >= len(text) || text[start] != '[' {
					return nil, 0, 0, fmt.Errorf("malformed OhMyKeymint scoop array")
				}
				end, err := tomlArrayEnd(text, start)
				if err != nil {
					return nil, 0, 0, err
				}
				apps, err := parseTomlStrings(text[start+1 : end])
				return apps, start, end + 1, err
			}
		}
		offset += len(line)
	}
	return nil, 0, 0, fmt.Errorf("OhMyKeymint injector.toml has no scoop array")
}

func tomlArrayEnd(text string, open int) (int, error) {
	depth := 0
	inString := false
	for i := open; i < len(text); i++ {
		c := text[i]
		if inString {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated OhMyKeymint scoop array")
}

func parseTomlStrings(body string) ([]string, error) {
	out := []string{}
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '#':
			for i < len(body) && body[i] != '\n' {
				i++
			}
		case c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(body) && body[j] != '"' {
				if body[j] == '\\' && j+1 < len(body) {
					switch body[j+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					default:
						b.WriteByte(body[j+1])
					}
					j += 2
					continue
				}
				b.WriteByte(body[j])
				j++
			}
			if j >= len(body) {
				return nil, fmt.Errorf("unterminated string in OhMyKeymint scoop")
			}
			out = append(out, b.String())
			i = j + 1
		default:
			return nil, fmt.Errorf("unsupported TOML value in OhMyKeymint scoop")
		}
	}
	return out, nil
}
