package runtime

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"keyboxlite/internal/backend"
)

func (e *engine) filesCandidate(s *filesStore, t backend.Target, id, revision string, restore bool) ([]byte, error) {
	if restore {
		if !filesBackupID(id, t.KeyboxPath) {
			return nil, fmt.Errorf("backup identifier does not belong to the selected target")
		}
		dir, err := filesOpenDir(filepath.Dir(e.host(t.KeyboxPath)))
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		data, _, err := filesRead(dir, id, filesMaxBytes, false)
		if err != nil {
			return nil, err
		}
		if filesRevision(t, data) != revision {
			return nil, fmt.Errorf("backup or selected backend changed; preview again")
		}
		return data, nil
	}
	sealed, _, err := filesRead(s.dir, id+".sealed", 64, true)
	if err != nil || string(sealed) != revision {
		return nil, fmt.Errorf("import must be inspected with the current revision")
	}
	data, _, err := filesRead(s.dir, id, filesMaxBytes, true)
	if err != nil {
		return nil, err
	}
	if filesRevision(t, data) != revision {
		return nil, fmt.Errorf("import or selected backend changed; preview again")
	}
	return data, nil
}

func (e *engine) filesInstall(parent context.Context, id, revision string, restore bool) (resultErr error) {
	ctx, cancel := context.WithTimeout(parent, filesBudget)
	defer cancel()
	if err := e.init(); err != nil {
		return fmt.Errorf("cannot initialize private storage")
	}
	action, err := lock(ctx, filepath.Join(e.p.state, "action.lock"), 0)
	if err != nil {
		return fmt.Errorf("another keybox action is running")
	}
	defer action.Close()
	if !e.enabled() {
		return fmt.Errorf("module is disabled or being removed")
	}
	finishLog, logErr := e.filesStartLog()
	if logErr != nil {
		e.say("[WARN] Could not open the persistent keybox log")
	}
	defer func() {
		if resultErr != nil {
			e.say("[FAIL] %v", resultErr)
		}
		if finishLog != nil {
			finishLog()
		}
	}()
	source := "local-import"
	if restore {
		source = "backup-restore"
	}
	e.say("[START] Keybox replacement: %s", source)
	t, err := e.target()
	if err != nil {
		return fmt.Errorf("no active supported backend or safe target")
	}
	s, err := e.filesStore(ctx)
	if err != nil {
		return err
	}
	data, err := e.filesCandidate(s, t, id, revision, restore)
	if err == nil && !restore {
		err = e.filesRefreshSession(s, id)
	}
	if err != nil {
		s.close()
		return err
	}
	work, err := e.filesNewWork(s)
	s.close()
	if err != nil {
		return err
	}
	defer work.close()
	if err = ctx.Err(); err != nil {
		return err
	}
	e.say("[CHECK] Preparing keybox for the selected backend")
	prepared, err := e.d.prepare(ctx, string(t.Backend), data, work.path)
	if err != nil || len(prepared) == 0 || len(prepared) > filesMaxBytes {
		return fmt.Errorf("keybox preparation failed; installed keybox retained")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = work.stage(prepared); err != nil {
		return err
	}
	file := filepath.Join(work.path, "candidate.xml")
	// A manual file always uses strict verification, independently of the
	// scheduler's saved skip policy. The production verifier reads this flag.
	previousSkip := e.skipRevocation
	e.skipRevocation = false
	defer func() { e.skipRevocation = previousSkip }()
	e.say("[CHECK] Strict certificate, validity and revocation verification")
	// Child diagnostics are not copied into this command's output: arbitrary
	// candidate XML and remote error text cannot become public import logs.
	code := e.d.verify(ctx, file, work.path, io.Discard)
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = work.check(prepared); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("strict verification failed (exit %d); installed keybox retained", code)
	}
	e.say("[OK] Strict keybox verification passed")
	// Recheck the exact input and backend after verification. Hold files.lock
	// through commit so discard/inspect cannot change a sealed session meanwhile.
	s, err = e.filesStore(ctx)
	if err != nil {
		return err
	}
	defer s.close()
	current, err := e.target()
	if err != nil || filesRevision(current, data) != revision {
		return fmt.Errorf("selected backend changed; preview again")
	}
	if _, err = e.filesCandidate(s, current, id, revision, restore); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = work.check(prepared); err != nil {
		return err
	}
	e.say("[INSTALL] Installing verified keybox")
	if err = e.install(ctx, t, prepared); err != nil {
		return err
	}
	if err = e.filesHistory(source, len(prepared)); err != nil {
		e.say("[WARN] Keybox installed, but could not record update history")
	}
	if !restore {
		// Failed cleanup does not change the successful install result; TTL
		// cleanup or the UI's idempotent discard will remove remaining files.
		filesUnlink(s.dir, id+".sealed")
		filesUnlink(s.dir, id)
	}
	return nil
}
