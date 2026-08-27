package slogs

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// millRunOnce performs compression and removal of stale log files.
func (r *Rotator) millRunOnce() error {
	if r.MaxBackups == 0 && r.MaxAge == 0 && !r.Compress {
		return nil
	}

	files, err := r.oldLogFiles()
	if err != nil {
		return err
	}

	var compress, remove []logInfo
	if r.MaxBackups > 0 && r.MaxBackups < len(files) {
		preserved := make(map[string]bool)
		var remaining []logInfo
		for _, file := range files {
			name := file.Name()
			if strings.HasSuffix(name, compressSuffix) {
				name = name[:len(name)-len(compressSuffix)]
			}
			preserved[name] = true
			if len(preserved) > r.MaxBackups {
				remove = append(remove, file)
			} else {
				remaining = append(remaining, file)
			}
		}
		files = remaining
	}

	if r.MaxAge > 0 {
		diff := time.Duration(int64(24*time.Hour) * int64(r.MaxAge))
		cutoff := currentTime().Add(-diff)
		var remaining []logInfo
		for _, file := range files {
			if file.timestamp.Before(cutoff) {
				remove = append(remove, file)
			} else {
				remaining = append(remaining, file)
			}
		}
		files = remaining
	}

	if r.Compress {
		for _, file := range files {
			if !strings.HasSuffix(file.Name(), compressSuffix) {
				compress = append(compress, file)
			}
		}
	}

	for _, file := range remove {
		removeErr := os.Remove(filepath.Join(r.dir(), file.Name()))
		if err == nil && removeErr != nil {
			err = removeErr
		}
	}
	for _, file := range compress {
		name := filepath.Join(r.dir(), file.Name())
		compressErr := compressLogFile(name, name+compressSuffix)
		if err == nil && compressErr != nil {
			err = compressErr
		}
	}
	return err
}

// millRun processes cleanup requests until the Rotator is closed.
func (r *Rotator) millRun() {
	defer r.millWG.Done()
	for range r.millCh {
		_ = r.millRunOnce()
	}
}

// mill schedules post-rotation compression and removal.
func (r *Rotator) mill() {
	r.startMill.Do(func() {
		r.millCh = make(chan bool, 1)
		r.millWG.Add(1)
		go r.millRun()
	})
	select {
	case r.millCh <- true:
	default:
	}
}
