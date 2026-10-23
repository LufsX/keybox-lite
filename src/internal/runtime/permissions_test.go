package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRuntimeNewDirectoriesIgnoreUmask(t *testing.T) {
	e := fixture(t)
	wrapper := helperWrapper(t, e.p.root, "directory-helper", "mkdir-target")
	parent := filepath.Join(e.p.root, "existing")
	os.Mkdir(parent, 0700)
	child := filepath.Join(parent, "first", "second")
	if output, err := exec.Command(wrapper, child).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	for p, want := range map[string]os.FileMode{parent: 0700, filepath.Join(parent, "first"): 0755, child: 0755} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Fatalf("%s mode %o want %o", p, st.Mode().Perm(), want)
		}
	}
}
