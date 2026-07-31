package slogs

// This file is adapted from gopkg.in/natefinch/lumberjack.v2 (MIT License).
// Vendored to eliminate external dependency. Provides log file rotation with
// size-based rolling, backup retention, age-based cleanup, and gzip compression.

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	backupTimeFormat = "2006-01-02T15-04-05.000"
	compressSuffix   = ".gz"
	defaultMaxSize   = 100
)

// ensure Rotator always implements io.WriteCloser.
var _ io.WriteCloser = (*Rotator)(nil)

// Rotator is an io.WriteCloser that writes to a rotating log file.
//
// Rotator opens or creates the logfile on first Write. If the file exists and
// is less than MaxSize megabytes, it will open and append to that file.
// If the file exists and its size is >= MaxSize megabytes, the file is renamed
// by putting the current time in a timestamp in the name immediately before the
// file's extension. A new log file is then created using original filename.
//
// Backups use the form `name-timestamp.ext` where timestamp is formatted with
// `2006-01-02T15-04-05.000`.
type Rotator struct {
	// Filename is the file to write logs to. Backup log files will be retained
	// in the same directory.
	Filename string

	// MaxSize is the maximum size in megabytes of the log file before rotation.
	// Defaults to 100 megabytes.
	MaxSize int

	// MaxAge is the maximum number of days to retain old log files.
	MaxAge int

	// MaxBackups is the maximum number of old log files to retain.
	MaxBackups int

	// LocalTime determines if the time used for formatting the timestamps in
	// backup files is the computer's local time. Default is UTC.
	LocalTime bool

	// Compress determines if rotated log files should be compressed using gzip.
	Compress bool

	size int64
	file *os.File
	mu   sync.Mutex

	millCh    chan bool
	startMill sync.Once
}

var (
	// currentTime exists so it can be mocked out by tests.
	currentTime = time.Now

	// osStat exists so it can be mocked out by tests.
	osStat = os.Stat

	// megabyte is the conversion factor between MaxSize and bytes.
	megabyte = 1024 * 1024
)

// Write implements io.Writer. If a write would cause the log file to exceed
// MaxSize, the file is closed, renamed with a timestamp, and a new file created.
func (r *Rotator) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	writeLen := int64(len(p))
	if writeLen > r.max() {
		return 0, fmt.Errorf(
			"write length %d exceeds maximum file size %d", writeLen, r.max(),
		)
	}

	if r.file == nil {
		if err = r.openExistingOrNew(len(p)); err != nil {
			return 0, err
		}
	}

	if r.size+writeLen > r.max() {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}

	n, err = r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close implements io.Closer, and closes the current logfile.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.close()
}

// close closes the file if it is open.
func (r *Rotator) close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// Rotate causes Rotator to close the existing log file and immediately create
// a new one. After rotating, this initiates compression and removal of old log
// files according to the configuration.
func (r *Rotator) Rotate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rotate()
}

// rotate closes the current file, moves it aside with a timestamp in the name,
// opens a new file with the original filename, and runs post-rotation processing.
func (r *Rotator) rotate() error {
	if err := r.close(); err != nil {
		return err
	}
	if err := r.openNew(); err != nil {
		return err
	}
	r.mill()
	return nil
}

// openNew opens a new log file for writing, moving any old log file out of the way.
func (r *Rotator) openNew() error {
	err := os.MkdirAll(r.dir(), 0o755)
	if err != nil {
		return fmt.Errorf("can't make directories for new logfile: %s", err)
	}

	name := r.filename()
	mode := os.FileMode(0o600)
	info, err := osStat(name)
	if err == nil {
		mode = info.Mode()
		newname := backupName(name, r.LocalTime)
		if err := os.Rename(name, newname); err != nil {
			return fmt.Errorf("can't rename log file: %s", err)
		}
		if err := chown(name, info); err != nil {
			return err
		}
	}

	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("can't open new logfile: %s", err)
	}
	r.file = f
	r.size = 0
	return nil
}

// backupName creates a new filename from the given name, inserting a timestamp
// between the filename and the extension.
func backupName(name string, local bool) string {
	dir := filepath.Dir(name)
	filename := filepath.Base(name)
	ext := filepath.Ext(filename)
	prefix := filename[:len(filename)-len(ext)]
	t := currentTime()
	if !local {
		t = t.UTC()
	}
	timestamp := t.Format(backupTimeFormat)
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", prefix, timestamp, ext))
}

// openExistingOrNew opens the logfile if it exists and if the current write
// would not put it over MaxSize. Otherwise a new file is created.
func (r *Rotator) openExistingOrNew(writeLen int) error {
	r.mill()

	filename := r.filename()
	info, err := osStat(filename)
	if os.IsNotExist(err) {
		return r.openNew()
	}
	if err != nil {
		return fmt.Errorf("error getting log file info: %s", err)
	}

	if info.Size()+int64(writeLen) >= r.max() {
		return r.rotate()
	}

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return r.openNew()
	}
	r.file = file
	r.size = info.Size()
	return nil
}

// filename generates the name of the logfile.
func (r *Rotator) filename() string {
	if r.Filename != "" {
		return r.Filename
	}
	name := filepath.Base(os.Args[0]) + "-slogs.log"
	return filepath.Join(os.TempDir(), name)
}

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
		for _, f := range files {
			fn := f.Name()
			if strings.HasSuffix(fn, compressSuffix) {
				fn = fn[:len(fn)-len(compressSuffix)]
			}
			preserved[fn] = true
			if len(preserved) > r.MaxBackups {
				remove = append(remove, f)
			} else {
				remaining = append(remaining, f)
			}
		}
		files = remaining
	}

	if r.MaxAge > 0 {
		diff := time.Duration(int64(24*time.Hour) * int64(r.MaxAge))
		cutoff := currentTime().Add(-1 * diff)
		var remaining []logInfo
		for _, f := range files {
			if f.timestamp.Before(cutoff) {
				remove = append(remove, f)
			} else {
				remaining = append(remaining, f)
			}
		}
		files = remaining
	}

	if r.Compress {
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), compressSuffix) {
				compress = append(compress, f)
			}
		}
	}

	for _, f := range remove {
		errRemove := os.Remove(filepath.Join(r.dir(), f.Name()))
		if err == nil && errRemove != nil {
			err = errRemove
		}
	}
	for _, f := range compress {
		fn := filepath.Join(r.dir(), f.Name())
		errCompress := compressLogFile(fn, fn+compressSuffix)
		if err == nil && errCompress != nil {
			err = errCompress
		}
	}
	return err
}

// millRun runs in a goroutine to manage post-rotation compression and removal.
func (r *Rotator) millRun() {
	for range r.millCh {
		_ = r.millRunOnce()
	}
}

// mill performs post-rotation processing, starting the mill goroutine if needed.
func (r *Rotator) mill() {
	r.startMill.Do(func() {
		r.millCh = make(chan bool, 1)
		go r.millRun()
	})
	select {
	case r.millCh <- true:
	default:
	}
}

// oldLogFiles returns the list of backup log files sorted by timestamp (newest first).
func (r *Rotator) oldLogFiles() ([]logInfo, error) {
	entries, err := os.ReadDir(r.dir())
	if err != nil {
		return nil, fmt.Errorf("can't read log file directory: %s", err)
	}

	logFiles := []logInfo{}
	prefix, ext := r.prefixAndExt()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if t, err := r.timeFromName(entry.Name(), prefix, ext); err == nil {
			logFiles = append(logFiles, logInfo{t, info})
			continue
		}
		if t, err := r.timeFromName(entry.Name(), prefix, ext+compressSuffix); err == nil {
			logFiles = append(logFiles, logInfo{t, info})
			continue
		}
	}

	sort.Sort(byFormatTime(logFiles))
	return logFiles, nil
}

// timeFromName extracts the formatted time from the filename.
func (r *Rotator) timeFromName(filename, prefix, ext string) (time.Time, error) {
	if !strings.HasPrefix(filename, prefix) {
		return time.Time{}, errors.New("mismatched prefix")
	}
	if !strings.HasSuffix(filename, ext) {
		return time.Time{}, errors.New("mismatched extension")
	}
	ts := filename[len(prefix) : len(filename)-len(ext)]
	return time.Parse(backupTimeFormat, ts)
}

// max returns the maximum size in bytes of log files before rolling.
func (r *Rotator) max() int64 {
	if r.MaxSize == 0 {
		return int64(defaultMaxSize * megabyte)
	}
	return int64(r.MaxSize) * int64(megabyte)
}

// dir returns the directory for the current filename.
func (r *Rotator) dir() string {
	return filepath.Dir(r.filename())
}

// prefixAndExt returns the filename part and extension part.
func (r *Rotator) prefixAndExt() (prefix, ext string) {
	filename := filepath.Base(r.filename())
	ext = filepath.Ext(filename)
	prefix = filename[:len(filename)-len(ext)] + "-"
	return prefix, ext
}

// compressLogFile compresses the given log file, removing the original on success.
func compressLogFile(src, dst string) (err error) {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer f.Close()

	fi, err := osStat(src)
	if err != nil {
		return fmt.Errorf("failed to stat log file: %v", err)
	}

	if err := chown(dst, fi); err != nil {
		return fmt.Errorf("failed to chown compressed log file: %v", err)
	}

	gzf, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode())
	if err != nil {
		return fmt.Errorf("failed to open compressed log file: %v", err)
	}
	defer gzf.Close()

	gz := gzip.NewWriter(gzf)

	defer func() {
		// On error path, close gz and remove the partial output.
		if err != nil {
			gz.Close()
			os.Remove(dst)
			err = fmt.Errorf("failed to compress log file: %v", err)
		}
	}()

	if _, err = io.Copy(gz, f); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	// Close source before removing it (Windows requires this).
	if err = f.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// logInfo is a convenience struct to return the filename and its embedded timestamp.
type logInfo struct {
	timestamp time.Time
	os.FileInfo
}

// byFormatTime sorts by newest time formatted in the name.
type byFormatTime []logInfo

func (b byFormatTime) Less(i, j int) bool { return b[i].timestamp.After(b[j].timestamp) }
func (b byFormatTime) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }
func (b byFormatTime) Len() int           { return len(b) }
