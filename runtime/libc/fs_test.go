//go:build cgo && !windows

package libc

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestChdir(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errorNumber, available := Chdir(original); !available || errorNumber != 0 {
			t.Errorf("restore Chdir() = %d, %v", errorNumber, available)
		}
	}()

	directory := t.TempDir()
	if errorNumber, available := Chdir(directory); !available || errorNumber != 0 {
		t.Fatalf("Chdir() = %d, %v", errorNumber, available)
	}
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if current != directory {
		t.Fatalf("cwd = %q, want %q", current, directory)
	}
	if errorNumber, available := Chdir("missing"); !available || errorNumber == 0 {
		t.Fatalf("Chdir(missing) = %d, %v", errorNumber, available)
	}
	if err := os.WriteFile("plain", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if errorNumber, available := Chdir("plain"); !available || errorNumber != int(syscall.ENOTDIR) {
		t.Fatalf("Chdir(file) = %d, %v; want %d, true", errorNumber, available, syscall.ENOTDIR)
	}
}

func TestRealPath(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	resolved, errorNumber, available := RealPath(link)
	if !available || errorNumber != 0 || resolved != target {
		t.Fatalf("RealPath(link) = %q, %d, %v; want %q, 0, true", resolved, errorNumber, available, target)
	}
	if resolved, errorNumber, available := RealPath(filepath.Join(directory, "missing")); !available || errorNumber == 0 || resolved != "" {
		t.Fatalf("RealPath(missing) = %q, %d, %v", resolved, errorNumber, available)
	}
}

func TestFilesystemMetadata(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "file")
	if err := os.WriteFile(filename, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if errno, available := Chmod(filename, 0o640); !available || errno != 0 {
		t.Fatalf("Chmod() = %d, %v", errno, available)
	}
	information, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if information.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", information.Mode().Perm())
	}

	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	uid, err := strconv.ParseInt(current.Uid, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	resolvedUID, found, available := UserID(current.Username)
	if !available || !found || resolvedUID != uid {
		t.Fatalf("UserID(%q) = %d, %v, %v; want %d", current.Username, resolvedUID, found, available, uid)
	}
	if _, found, available := UserID("php2go-user-that-does-not-exist"); !available || found {
		t.Fatalf("missing UserID() found = %v, available = %v", found, available)
	}

	group, err := user.LookupGroupId(current.Gid)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.ParseInt(group.Gid, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	resolvedGID, found, available := GroupID(group.Name)
	if !available || !found || resolvedGID != gid {
		t.Fatalf("GroupID(%q) = %d, %v, %v; want %d", group.Name, resolvedGID, found, available, gid)
	}
	if errno, available := Chown(filename, uid, false, true); !available || errno != 0 {
		t.Fatalf("Chown(owner) = %d, %v", errno, available)
	}
	if errno, available := Chown(filename, gid, true, true); !available || errno != 0 {
		t.Fatalf("Chown(group) = %d, %v", errno, available)
	}

	link := filepath.Join(directory, "link")
	if err := os.Symlink("file", link); err != nil {
		t.Fatal(err)
	}
	if errno, available := Chown(link, uid, false, false); !available || errno != 0 {
		t.Fatalf("Lchown(owner) = %d, %v", errno, available)
	}

	touched := filepath.Join(directory, "touched")
	if errno, stage, available := Touch(touched, true, 123456789, 123456700); !available || errno != 0 || stage != TouchComplete {
		t.Fatalf("Touch(create) = %d, %d, %v", errno, stage, available)
	}
	information, err = os.Stat(touched)
	if err != nil {
		t.Fatal(err)
	}
	if information.ModTime().Unix() != 123456789 {
		t.Fatalf("mtime = %d, want 123456789", information.ModTime().Unix())
	}
	before := time.Now().Unix()
	if errno, stage, available := Touch(touched, false, 0, 0); !available || errno != 0 || stage != TouchComplete {
		t.Fatalf("Touch(now) = %d, %d, %v", errno, stage, available)
	}
	information, err = os.Stat(touched)
	if err != nil {
		t.Fatal(err)
	}
	if information.ModTime().Unix() < before {
		t.Fatalf("mtime = %d, want >= %d", information.ModTime().Unix(), before)
	}
}

func TestReadDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "alpha"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	names, errorNumber, available := ReadDirectory(directory)
	if !available || errorNumber != 0 {
		t.Fatalf("ReadDirectory() errno = %d, available = %v", errorNumber, available)
	}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	for _, name := range []string{".", "..", "alpha", "nested"} {
		if !seen[name] {
			t.Errorf("ReadDirectory() = %#v, missing %q", names, name)
		}
	}
	if _, errorNumber, available := ReadDirectory(filepath.Join(directory, "missing")); !available || errorNumber == 0 {
		t.Fatalf("ReadDirectory(missing) errno = %d, available = %v", errorNumber, available)
	}
}

func TestLiveDirectory(t *testing.T) {
	directory := t.TempDir()
	handle, errorNumber, available := OpenDirectory(directory)
	if !available || errorNumber != 0 || handle == nil {
		t.Fatalf("OpenDirectory() = %#v, %d, %v", handle, errorNumber, available)
	}
	defer handle.Close()
	if err := os.WriteFile(filepath.Join(directory, "after-open"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		name, errorNumber, ok := handle.Read()
		if !ok {
			if errorNumber != 0 {
				t.Fatalf("Directory.Read() errno = %d", errorNumber)
			}
			break
		}
		seen[name] = true
	}
	if !seen["after-open"] {
		t.Fatalf("live directory entries = %#v", seen)
	}
	handle.Rewind()
	if name, errorNumber, ok := handle.Read(); !ok || errorNumber != 0 || name != "." {
		t.Fatalf("Directory.Read() after rewind = %q, %d, %v", name, errorNumber, ok)
	}
}

func TestMakeAndRemoveDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created")
	if errorNumber, available := Mkdir(path, 0o750); !available || errorNumber != 0 {
		t.Fatalf("Mkdir() = %d, %v", errorNumber, available)
	}
	information, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !information.IsDir() || information.Mode().Perm() != 0o750 {
		t.Fatalf("created directory = %v, mode %o", information.IsDir(), information.Mode().Perm())
	}
	if errorNumber, available := Rmdir(path); !available || errorNumber != 0 {
		t.Fatalf("Rmdir() = %d, %v", errorNumber, available)
	}
}

func TestCreateLinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(directory, "hard")
	if errorNumber, available := Link(target, hard); !available || errorNumber != 0 {
		t.Fatalf("Link() = %d, %v", errorNumber, available)
	}
	contents, err := os.ReadFile(hard)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "content" {
		t.Fatalf("hard-link contents = %q", contents)
	}

	soft := filepath.Join(directory, "soft")
	if errorNumber, available := Symlink("target", soft); !available || errorNumber != 0 {
		t.Fatalf("Symlink() = %d, %v", errorNumber, available)
	}
	stored, err := os.Readlink(soft)
	if err != nil {
		t.Fatal(err)
	}
	if stored != "target" {
		t.Fatalf("symbolic-link target = %q, want target", stored)
	}
}

func TestTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	oldMask, available := Umask(0, true)
	if !available {
		t.Fatal("Umask is unavailable")
	}
	defer Umask(int64(oldMask), true)

	file, path, errorNumber, available := TemporaryFile(directory, "prefix")
	if !available || errorNumber != 0 || file == nil {
		t.Fatalf("TemporaryFile() = %v, %q, %d, %v",
			file, path, errorNumber, available)
	}
	defer os.Remove(path)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != directory {
		t.Fatalf("temporary directory = %q, want %q", filepath.Dir(path), directory)
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "prefix") || len(name) != len("prefix")+19 {
		t.Fatalf("temporary basename = %q", name)
	}
	information, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if information.Mode().Perm() != 0o600 {
		t.Fatalf("temporary mode = %o, want 600", information.Mode().Perm())
	}

	if file, _, errorNumber, available := TemporaryFile(
		filepath.Join(directory, "missing"), "prefix",
	); !available || errorNumber == 0 || file != nil {
		t.Fatalf("TemporaryFile(missing) = %v, errno %d, available %v",
			file, errorNumber, available)
	}
}

func TestRename(t *testing.T) {
	directory := t.TempDir()
	from := filepath.Join(directory, "from")
	to := filepath.Join(directory, "to")
	if err := os.WriteFile(from, []byte("content"), 0o640); err != nil {
		t.Fatal(err)
	}
	if errorNumber, sourceDirectory, warningNumber, available :=
		Rename(from, to); !available || errorNumber != 0 || sourceDirectory || warningNumber != 0 {
		t.Fatalf("Rename() = %d, %v, %d, %v",
			errorNumber, sourceDirectory, warningNumber, available)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("source stat error = %v, want not exist", err)
	}
	contents, err := os.ReadFile(to)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "content" {
		t.Fatalf("renamed contents = %q", contents)
	}
}

func TestRenameAcrossDevices(t *testing.T) {
	shared, err := os.Stat("/dev/shm")
	if err != nil {
		t.Skipf("/dev/shm unavailable: %v", err)
	}
	local, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sharedStat, sharedOK := shared.Sys().(*syscall.Stat_t)
	localStat, localOK := local.Sys().(*syscall.Stat_t)
	if !sharedOK || !localOK || sharedStat.Dev == localStat.Dev {
		t.Skip("no distinct /dev/shm filesystem")
	}

	destination, err := os.CreateTemp("/dev/shm", "php2go-rename-*")
	if err != nil {
		t.Skipf("cannot create /dev/shm destination: %v", err)
	}
	destinationName := destination.Name()
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(destinationName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(destinationName) })

	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("cross-device"), 0o641); err != nil {
		t.Fatal(err)
	}
	if errorNumber, sourceDirectory, warningNumber, available :=
		Rename(source, destinationName); !available || errorNumber != 0 ||
		sourceDirectory || warningNumber != 0 {
		t.Fatalf("Rename(cross-device) = %d, %v, %d, %v",
			errorNumber, sourceDirectory, warningNumber, available)
	}
	information, err := os.Stat(destinationName)
	if err != nil {
		t.Fatal(err)
	}
	if information.Mode().Perm() != 0o641 {
		t.Fatalf("cross-device mode = %o, want 641", information.Mode().Perm())
	}
	contents, err := os.ReadFile(destinationName)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "cross-device" {
		t.Fatalf("cross-device contents = %q", contents)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("cross-device source stat error = %v, want not exist", err)
	}

	directorySource := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(directorySource, 0o755); err != nil {
		t.Fatal(err)
	}
	directoryDestination := destinationName + "-directory"
	t.Cleanup(func() { _ = os.Remove(directoryDestination) })
	errorNumber, sourceDirectory, _, available :=
		Rename(directorySource, directoryDestination)
	if !available || errorNumber != int(syscall.EXDEV) || !sourceDirectory {
		t.Fatalf("Rename(cross-device directory) = %d, %v, %v",
			errorNumber, sourceDirectory, available)
	}
}

func TestUmask(t *testing.T) {
	original, available := Umask(0, false)
	if !available {
		t.Fatal("Umask(query) unavailable")
	}
	t.Cleanup(func() {
		if _, available := Umask(int64(original), true); !available {
			t.Error("Umask(restore) unavailable")
		}
	})
	if old, available := Umask(0o027, true); !available || old != original {
		t.Fatalf("Umask(set) = %o, %v; want %o, true", old, available, original)
	}
	if current, available := Umask(0, false); !available || current != 0o027 {
		t.Fatalf("Umask(query after set) = %o, %v; want 027, true", current, available)
	}
	if old, available := Umask(-1, true); !available || old != 0o027 {
		t.Fatalf("Umask(-1) = %o, %v; want 027, true", old, available)
	}
	if current, available := Umask(0, false); !available || current != 0o777 {
		t.Fatalf("Umask(query after -1) = %o, %v; want 0777, true", current, available)
	}
}
