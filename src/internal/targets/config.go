package targets

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type section struct {
	name  string
	lines []string
}
type document struct {
	config   Config
	sections []section
	legacy   bool
	target   snapshot
	patch    snapshot
}

func parseTarget(line string) (Target, error) {
	t := Target{line, "auto"}
	if strings.HasSuffix(line, "!") {
		t.PackageName = strings.TrimSpace(strings.TrimSuffix(line, "!"))
		t.Mode = "generate"
	}
	if strings.HasSuffix(line, "?") {
		t.PackageName = strings.TrimSpace(strings.TrimSuffix(line, "?"))
		t.Mode = "hack"
	}
	if !validName(t.PackageName) {
		return t, fmt.Errorf("invalid target %q", line)
	}
	return t, nil
}
func contentLine(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && !strings.HasPrefix(s, "#") && !strings.HasPrefix(s, ";")
}
func checkText(data []byte) error {
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return fmt.Errorf("invalid config encoding")
	}
	for _, c := range string(data) {
		if c < 32 && c != '\n' && c != '\r' && c != '\t' {
			return fmt.Errorf("invalid control character")
		}
	}
	return nil
}
func readDocument(d *disk, legacy bool) (document, error) {
	doc := document{config: emptyConfig(), legacy: legacy}
	name := "config.ini"
	if legacy {
		name = "target.txt"
	}
	var err error
	doc.target, err = d.read("/data/adb/tricky_store/"+name, false)
	if err != nil {
		return doc, err
	}
	if err = checkText(doc.target.Data); err != nil {
		return doc, err
	}
	if legacy {
		doc.patch, err = d.read("/data/adb/tricky_store/security_patch.txt", false)
		if err != nil {
			return doc, err
		}
		if err = checkText(doc.patch.Data); err != nil {
			return doc, err
		}
		for _, line := range strings.Split(string(doc.target.Data), "\n") {
			if !contentLine(line) {
				continue
			}
			t, e := parseTarget(strings.TrimSpace(line))
			if e != nil {
				return doc, e
			}
			doc.config.Targets = append(doc.config.Targets, t)
		}
		doc.sections = []section{{name: "legacy", lines: strings.Split(string(doc.patch.Data), "\n")}}
		for _, line := range doc.sections[0].lines {
			if !contentLine(line) {
				continue
			}
			line = strings.TrimSpace(line)
			k, v, ok := strings.Cut(line, "=")
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if !ok {
				k = "all"
				v = line
			}
			switch k {
			case "all":
				for _, key := range []string{"os_patch", "vendor_patch", "boot_patch"} {
					doc.config.DefaultPolicy[key] = legacyPatch(key, v)
				}
			case "system":
				doc.config.DefaultPolicy["os_patch"] = legacyPatch("os_patch", v)
			case "vendor":
				doc.config.DefaultPolicy["vendor_patch"] = legacyPatch("vendor_patch", v)
			case "boot":
				doc.config.DefaultPolicy["boot_patch"] = legacyPatch("boot_patch", v)
			default:
				if !ok || k == "" || v == "" {
					return doc, fmt.Errorf("malformed security_patch.txt")
				}
			}
		}
	} else {
		doc.sections = []section{{}}
		// Resolve single-segment application sections even when they precede [target].
		var allTargets []Target
		inTargets := false
		for _, line := range strings.Split(string(doc.target.Data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				inTargets = line == "[target]"
				continue
			}
			if inTargets && contentLine(line) {
				if t, e := parseTarget(line); e == nil {
					allTargets = append(allTargets, t)
				}
			}
		}
		seen := map[string]bool{}
		for _, line := range strings.Split(string(doc.target.Data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") {
				if !strings.HasSuffix(trimmed, "]") || strings.Count(trimmed, "[") != 1 || strings.Count(trimmed, "]") != 1 {
					return doc, fmt.Errorf("malformed INI section")
				}
				n := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
				if n == "" || seen[n] {
					return doc, fmt.Errorf("empty or duplicate INI section")
				}
				seen[n] = true
				doc.sections = append(doc.sections, section{name: n})
				continue
			}
			sec := &doc.sections[len(doc.sections)-1]
			sec.lines = append(sec.lines, line)
			if !contentLine(line) {
				continue
			}
			if sec.name == "target" {
				t, e := parseTarget(trimmed)
				if e != nil {
					return doc, e
				}
				doc.config.Targets = append(doc.config.Targets, t)
				continue
			}
			if sec.name == "" {
				return doc, fmt.Errorf("INI content outside a section")
			}
			k, v, ok := strings.Cut(trimmed, "=")
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if !ok || k == "" {
				return doc, fmt.Errorf("malformed INI key")
			}
			if !knownKey(k) {
				continue
			}
			if sec.name == "default_policy" {
				if _, ok := doc.config.DefaultPolicy[k]; ok {
					return doc, fmt.Errorf("duplicate patch key")
				}
				doc.config.DefaultPolicy[k] = v
			} else if appSection(sec.name, allTargets) {
				if doc.config.PerAppPolicy[sec.name] == nil {
					doc.config.PerAppPolicy[sec.name] = Policy{}
				}
				if _, ok := doc.config.PerAppPolicy[sec.name][k]; ok {
					return doc, fmt.Errorf("duplicate patch key")
				}
				doc.config.PerAppPolicy[sec.name][k] = v
			}
		}
	}
	if err = validateConfig(&doc.config); err != nil {
		return doc, err
	}
	return doc, nil
}

// Legacy configurations also use Android's YYYY-MM-DD property format.
// The shared CLI/WebUI policy uses compact dates; validate before removing dashes.
func legacyPatch(key, value string) string {
	if len(value) == 10 {
		if date, err := time.Parse("2006-01-02", value); err == nil {
			if key == "os_patch" {
				return date.Format("200601")
			}
			return date.Format("20060102")
		}
	}
	if key == "os_patch" && len(value) == 8 && validPatch("boot_patch", value) {
		return value[:6]
	}
	return value
}

func (doc document) legacyValues() Policy {
	values := Policy{}
	for _, line := range doc.sections[0].lines {
		if !contentLine(line) {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok {
			key, value = "all", strings.TrimSpace(line)
		}
		switch key {
		case "all":
			for _, name := range []string{"os_patch", "vendor_patch", "boot_patch"} {
				values[name] = value
			}
		case "system":
			values["os_patch"] = value
		case "vendor":
			values["vendor_patch"] = value
		case "boot":
			values["boot_patch"] = value
		}
	}
	return values
}

func keys(p Policy) []string {
	k := make([]string, 0, len(p))
	for s := range p {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}
func writePolicy(p Policy) []string {
	out := []string{}
	for _, k := range keys(p) {
		out = append(out, k+" = "+p[k])
	}
	return out
}
func targetLines(c Config) []string {
	out := []string{}
	for _, t := range c.Targets {
		out = append(out, t.PackageName+modeMarker(t.Mode))
	}
	return out
}
func (doc document) changes(c Config) []change {
	name := "/data/adb/tricky_store/config.ini"
	var lines []string
	if doc.legacy {
		name = "/data/adb/tricky_store/target.txt"
		// Preserve comments from the target file.
		for _, line := range strings.Split(string(doc.target.Data), "\n") {
			if !contentLine(line) && strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		lines = append(lines, targetLines(c)...)
		patchLines := []string{}
		for _, line := range doc.sections[0].lines {
			if !contentLine(line) {
				if strings.TrimSpace(line) != "" {
					patchLines = append(patchLines, line)
				}
				continue
			}
			k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
			k = strings.TrimSpace(k)
			if ok && k != "system" && k != "vendor" && k != "boot" && k != "all" {
				patchLines = append(patchLines, line)
			}
		}
		existing := doc.legacyValues()
		for _, k := range keys(c.DefaultPolicy) {
			native := map[string]string{"os_patch": "system", "vendor_patch": "vendor", "boot_patch": "boot"}[k]
			value := c.DefaultPolicy[k]
			// Keep existing spellings when only targets or other policies change.
			if raw, ok := existing[k]; ok && legacyPatch(k, raw) == value {
				value = raw
			}
			patchLines = append(patchLines, native+"="+value)
		}
		return []change{{name, doc.target, fileImage([]byte(strings.Join(lines, "\n")+"\n"), doc.target, false)},
			{"/data/adb/tricky_store/security_patch.txt", doc.patch, fileImage([]byte(strings.Join(patchLines, "\n")+"\n"), doc.patch, false)}}
	}
	seen := map[string]bool{}
	for _, sec := range doc.sections {
		seen[sec.name] = true
		if sec.name != "" {
			lines = append(lines, "["+sec.name+"]")
		}
		if sec.name == "target" {
			for _, line := range sec.lines {
				if !contentLine(line) && strings.TrimSpace(line) != "" {
					lines = append(lines, line)
				}
			}
			lines = append(lines, targetLines(c)...)
			continue
		}
		editable := sec.name == "default_policy" || appSection(sec.name, doc.config.Targets) || appSection(sec.name, c.Targets)
		for _, line := range sec.lines {
			k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
			if editable && ok && knownKey(strings.TrimSpace(k)) {
				continue
			}
			lines = append(lines, line)
		}
		if sec.name == "default_policy" {
			lines = append(lines, writePolicy(c.DefaultPolicy)...)
		} else if editable {
			lines = append(lines, writePolicy(c.PerAppPolicy[sec.name])...)
		}
	}
	if !seen["target"] {
		lines = append(lines, "[target]")
		lines = append(lines, targetLines(c)...)
	}
	if !seen["default_policy"] && len(c.DefaultPolicy) > 0 {
		lines = append(lines, "[default_policy]")
		lines = append(lines, writePolicy(c.DefaultPolicy)...)
	}
	names := make([]string, 0, len(c.PerAppPolicy))
	for n := range c.PerAppPolicy {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !seen[n] && len(c.PerAppPolicy[n]) > 0 {
			lines = append(lines, "["+n+"]")
			lines = append(lines, writePolicy(c.PerAppPolicy[n])...)
		}
	}
	return []change{{name, doc.target, fileImage([]byte(strings.Join(lines, "\n")+"\n"), doc.target, false)}}
}
