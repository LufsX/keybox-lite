package targets

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func Run(args []string) int { return run(args, os.Stdin, os.Stdout, os.Stderr) }
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root, module := "/", ""
	pos := []string{}
	bad := func() int {
		fmt.Fprintln(stderr, "usage: targets [--root <sysroot>] [--module-dir <device module path>] status|packages|save|auto|entry status|set on|off|apply|remove")
		return 2
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root", "--module-dir":
			flag := args[i]
			i++
			if i >= len(args) || args[i] == "" {
				return bad()
			}
			if flag == "--root" {
				root = args[i]
			} else {
				module = args[i]
			}
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) == 0 {
		return bad()
	}
	command := pos[0]
	if command == "entry" {
		if !(len(pos) == 2 && (pos[1] == "status" || pos[1] == "apply" || pos[1] == "remove")) && !(len(pos) == 3 && pos[1] == "set" && (pos[2] == "on" || pos[2] == "off")) {
			return bad()
		}
	} else if len(pos) != 1 || (command != "status" && command != "packages" && command != "save" && command != "auto") {
		return bad()
	}
	fail := func(err error) int { fmt.Fprintln(stderr, "targets:", err); return 1 }
	m, err := newManager(root, module)
	if err != nil {
		return fail(err)
	}
	defer m.close()
	var result any = struct {
		OK bool `json:"ok"`
	}{true}
	switch command {
	case "status", "packages":
		var s Status
		s, err = m.status()
		if command == "packages" {
			result = s.Packages
		} else {
			result = s
		}
	case "save":
		var data []byte
		data, err = io.ReadAll(io.LimitReader(stdin, maxIO+1))
		if err == nil {
			var req SaveRequest
			if err = strictJSON(data, &req); err == nil {
				err = m.save(req)
			}
		}
	case "auto":
		err = m.auto()
	case "entry":
		switch pos[1] {
		case "status":
			err = m.locked(func() error {
				st, _, e := m.loadState()
				if e != nil {
					return e
				}
				t, _, e := m.detect()
				if e != nil {
					return e
				}
				entry, e := m.entryStatus(st, t)
				result = entry
				return e
			})
		case "set":
			err = m.entrySet(pos[2] == "on")
		case "apply":
			err = m.entryApply()
		case "remove":
			err = m.entrySet(false)
		}
	}
	if err != nil {
		return fail(err)
	}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		return fail(err)
	}
	return 0
}
