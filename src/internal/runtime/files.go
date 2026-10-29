package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"keyboxlite/internal/backend"
	"keyboxlite/internal/keybox"
)

// Preview contains public certificate information only. Revocation is always
// not_checked here; only installation performs the strict online verification.
type Preview struct {
	ID             string            `json:"id"`
	Size           int64             `json:"size"`
	Modified       int64             `json:"modified"`
	Revision       string            `json:"revision"`
	StructureValid bool              `json:"structure_valid"`
	Compatible     bool              `json:"compatible"`
	Serials        []string          `json:"serials"`
	Validity       *keybox.Validity  `json:"validity"`
	Integrity      *keybox.Integrity `json:"integrity"`
	Error          string            `json:"error,omitempty"`
	Backend        backend.Target    `json:"backend"`
	Target         string            `json:"target"`
	Revocation     string            `json:"revocation"`
}

type filesBackups struct {
	Backend backend.Target `json:"backend"`
	Target  string         `json:"target"`
	Backups []Preview      `json:"backups"`
}

func filesArgs(args []string) bool {
	if len(args) < 2 || args[0] == "" || !filepath.IsAbs(args[0]) || strings.ContainsRune(args[0], 0) {
		return false
	}
	switch args[1] {
	case "backups", "create":
		return len(args) == 2
	case "append":
		if len(args) != 4 || !filesID(args[2]) || args[3] == "" || strings.Trim(args[3], "0123456789") != "" {
			return false
		}
		offset, err := strconv.ParseInt(args[3], 10, 64)
		return err == nil && offset <= filesMaxBytes
	case "inspect":
		return len(args) == 3 && (filesID(args[2]) || filesBackupName(args[2]))
	case "discard":
		return len(args) == 3 && filesID(args[2])
	case "install":
		return len(args) == 4 && filesID(args[2]) && filesID(args[3])
	case "restore":
		// The exact basename is checked against the selected target under lock.
		return len(args) == 4 && filepath.Base(args[2]) == args[2] && strings.Contains(args[2], ".bak.") && filesID(args[3])
	}
	return false
}

// Files accepts [module-absolute-dir, operation, ...]. Production writes have no
// sysroot override. Upload data is accepted only on stdin as one base64 chunk.
func Files(args []string) int {
	if !filesArgs(args) {
		fmt.Fprintln(os.Stderr, "usage: keybox-files <module-absolute-dir> backups|create|append <id> <offset-bytes>|inspect <id>|install <id> <revision>|restore <backup-id> <revision>|discard <id>")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, filesBudget)
	defer cancel()
	if err := production(args[0]).files(ctx, args[1:], os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}

func (e *engine) files(ctx context.Context, args []string, in io.Reader) error {
	if !filesArgs(append([]string{e.p.module}, args...)) {
		return fmt.Errorf("invalid keybox-files arguments")
	}
	if args[0] == "install" || args[0] == "restore" {
		return e.filesInstall(ctx, args[1], args[2], args[0] == "restore")
	}
	// Read the bounded stdin before taking the storage lock.
	var chunk []byte
	if args[0] == "append" {
		encoded, err := io.ReadAll(io.LimitReader(in, int64(base64.StdEncoding.EncodedLen(filesChunkBytes))+1))
		if err != nil || len(encoded) > base64.StdEncoding.EncodedLen(filesChunkBytes) {
			return fmt.Errorf("base64 chunk exceeds size limit or cannot be read")
		}
		chunk, err = base64.StdEncoding.Strict().DecodeString(string(encoded))
		if err != nil || len(chunk) == 0 || len(chunk) > filesChunkBytes {
			return fmt.Errorf("invalid base64 chunk")
		}
	}
	s, err := e.filesStore(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	if err = ctx.Err(); err != nil {
		return err
	}
	switch args[0] {
	case "create":
		return e.filesCreate(s)
	case "append":
		offset, _ := strconv.ParseInt(args[2], 10, 64)
		size, err := e.filesAppend(s, args[1], offset, chunk)
		if err != nil {
			return err
		}
		return e.filesJSON(struct {
			OK   bool  `json:"ok"`
			Size int64 `json:"size"`
		}{true, size})
	case "discard":
		// Unlinking controlled basenames never opens or follows their contents.
		if err = filesUnlink(s.dir, args[1]+".sealed"); err == nil {
			err = filesUnlink(s.dir, args[1])
		}
		if err != nil {
			return err
		}
		return e.filesJSON(struct {
			OK bool `json:"ok"`
		}{true})
	case "inspect", "backups":
		t, err := e.target()
		if err != nil {
			return fmt.Errorf("no active supported backend or safe target")
		}
		if args[0] == "backups" {
			return e.filesList(t)
		}
		if !filesID(args[1]) {
			if !filesBackupID(args[1], t.KeyboxPath) {
				return fmt.Errorf("backup identifier does not belong to the selected target")
			}
			dir, err := filesOpenDir(filepath.Dir(e.host(t.KeyboxPath)))
			if err != nil {
				return err
			}
			defer dir.Close()
			data, st, err := filesRead(dir, args[1], filesMaxBytes, false)
			if err != nil {
				return err
			}
			return e.filesJSON(e.filesPreview(args[1], t, data, st))
		}
		data, st, err := filesRead(s.dir, args[1], filesMaxBytes, true)
		if err != nil {
			return err
		}
		p := e.filesPreview(args[1], t, data, st)
		if err = e.filesSeal(s, args[1], p.Revision); err != nil {
			return err
		}
		return e.filesJSON(p)
	}
	return fmt.Errorf("unknown file operation")
}

func (e *engine) filesJSON(value any) error {
	return json.NewEncoder(e.out).Encode(value)
}

func (e *engine) filesCreate(s *filesStore) error {
	names, err := filesNames(s.dir)
	if err != nil {
		return err
	}
	count := 0
	for _, name := range names {
		if filesID(name) {
			count++
		}
	}
	if count >= filesSessionLimit {
		return fmt.Errorf("too many active import sessions")
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return fmt.Errorf("cannot create import identifier")
	}
	id := hex.EncodeToString(token[:])
	f, err := filesOpen(s.dir, id, syscall.O_CREAT|syscall.O_EXCL|syscall.O_WRONLY, true)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		filesUnlink(s.dir, id)
		return fmt.Errorf("cannot create import file")
	}
	if err = e.filesJSON(struct {
		ID string `json:"id"`
	}{id}); err != nil {
		filesUnlink(s.dir, id)
		return err
	}
	return nil
}

func (e *engine) filesAppend(s *filesStore, id string, offset int64, chunk []byte) (int64, error) {
	sealed, err := filesOpen(s.dir, id+".sealed", syscall.O_RDONLY, true)
	if err == nil {
		sealed.Close()
		return 0, fmt.Errorf("import session is sealed")
	}
	if !os.IsNotExist(err) {
		return 0, err
	}
	f, err := filesOpen(s.dir, id, syscall.O_RDWR, true)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() != offset {
		return 0, fmt.Errorf("import offset does not match current size")
	}
	if offset+int64(len(chunk)) > filesMaxBytes {
		return 0, fmt.Errorf("import exceeds 1 MiB")
	}
	n, err := f.WriteAt(chunk, offset)
	if err == nil && n == len(chunk) {
		err = f.Sync()
	} else if err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		// A failed append does not consume its offset. If rollback fails, remove
		// the session rather than leaving ambiguous data available to install.
		if f.Truncate(offset) != nil || f.Sync() != nil {
			filesUnlink(s.dir, id)
		}
		return 0, fmt.Errorf("cannot append import chunk")
	}
	return offset + int64(n), nil
}

func (e *engine) filesSeal(s *filesStore, id, revision string) error {
	f, err := filesOpen(s.dir, id+".sealed", syscall.O_CREAT|syscall.O_RDWR, true)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = f.Truncate(0); err == nil {
		_, err = f.WriteString(revision)
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return fmt.Errorf("cannot seal import session")
	}
	return nil
}

func filesRevision(t backend.Target, data []byte) string {
	h := sha256.New()
	metadata, _ := json.Marshal(t)
	h.Write([]byte("keybox-files-v1\x00"))
	h.Write(metadata)
	h.Write([]byte{0})
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func (e *engine) filesPreview(id string, t backend.Target, data []byte, st os.FileInfo) Preview {
	p := Preview{ID: id, Size: st.Size(), Modified: st.ModTime().Unix(), Revision: filesRevision(t, data),
		Serials: []string{}, Backend: t, Target: t.KeyboxPath, Revocation: "not_checked"}
	// Prepare raw input before any certificate extraction. StructureValid alone
	// accepts XML containing malformed DER that must never reach serial parsing.
	prepared, err := keybox.Prepare(data, string(t.Backend))
	if err != nil {
		p.StructureValid = keybox.StructureValid(data)
		p.Error = "keybox is not compatible with the selected backend"
		if !p.StructureValid {
			p.Error = "invalid keybox XML"
		}
		return p
	}
	p.StructureValid = true
	p.Compatible = true
	p.Serials = keybox.Serials(prepared)
	validity, _ := keybox.CheckValidity(prepared, e.d.now())
	p.Validity = &validity
	integrity := keybox.CheckIntegrity(prepared)
	p.Integrity = &integrity
	return p
}

func (e *engine) filesList(t backend.Target) error {
	result := filesBackups{Backend: t, Target: t.KeyboxPath, Backups: []Preview{}}
	dir, err := filesOpenDir(filepath.Dir(e.host(t.KeyboxPath)))
	if err != nil {
		// A backend with no keybox directory has no backups. Other failures are
		// reported so an unsafe directory cannot look like a successful scan.
		if _, statErr := os.Lstat(filepath.Dir(e.host(t.KeyboxPath))); os.IsNotExist(statErr) {
			return e.filesJSON(result)
		}
		return err
	}
	defer dir.Close()
	names, err := filesNames(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !filesBackupID(name, t.KeyboxPath) {
			continue
		}
		data, st, err := filesRead(dir, name, filesMaxBytes, false)
		if err != nil {
			// Never list links, devices, FIFOs, or files replaced during read.
			// Oversized regular files can be described without reading them.
			if st != nil && st.Size() > filesMaxBytes {
				result.Backups = append(result.Backups, Preview{ID: name, Size: st.Size(), Modified: st.ModTime().Unix(),
					Serials: []string{}, Backend: t, Target: t.KeyboxPath, Revocation: "not_checked", Error: "file exceeds size limit"})
			}
			continue
		}
		result.Backups = append(result.Backups, e.filesPreview(name, t, data, st))
	}
	sort.Slice(result.Backups, func(i, j int) bool {
		a, b := result.Backups[i], result.Backups[j]
		if a.Modified == b.Modified {
			return a.ID > b.ID
		}
		return a.Modified > b.Modified
	})
	return e.filesJSON(result)
}
