package interp

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// realpathCacheBucket mirrors the observable part of Zend's virtual-cwd
// bucket. sequence records insertion order because collisions are linked at
// the head of each of the engine's 1,024 hash buckets.
type realpathCacheBucket struct {
	path     string
	realpath string
	key      uint64
	expires  int64
	isDir    bool
	sequence uint64
	size     int64
}

const realpathCacheBucketSize = int64(48)

func realpathCacheKey(path string) uint64 {
	hash := uint64(2166136261)
	for index := 0; index < len(path); index++ {
		hash *= 16777619
		hash ^= uint64(path[index])
	}
	return hash
}

func absoluteRealpathInput(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(workingDirectory, string(os.PathSeparator)) {
		return workingDirectory + path, nil
	}
	return workingDirectory + string(os.PathSeparator) + path, nil
}

// realpathPrefixes retains repeated separators and dot segments in the cache
// key. A component spelled . or .. is not itself cached, but the next ordinary
// component keeps that spelling in its prefix, matching virtual_file_ex().
func realpathPrefixes(path string) []string {
	result := make([]string, 0, 8)
	for position := 0; position < len(path); {
		for position < len(path) && path[position] == '/' {
			position++
		}
		start := position
		for position < len(path) && path[position] != '/' {
			position++
		}
		if start == position {
			continue
		}
		component := path[start:position]
		if component == "." || component == ".." {
			continue
		}
		prefix := path[:position]
		if prefix != "" && prefix != "/" {
			result = append(result, prefix)
		}
	}
	return result
}

func (unit *Unit) realpathCacheLimit() int64 {
	return stdlib.IniQuantity(unit.setting("realpath_cache_size"))
}

func (unit *Unit) realpathCacheTTL() int64 {
	return stdlib.IniQuantity(unit.setting("realpath_cache_ttl"))
}

func (unit *Unit) removeRealpathCache(path string) {
	if unit.realpathCache == nil {
		return
	}
	if held := unit.realpathCache[path]; held != nil {
		unit.realpathCacheSize -= held.size
		delete(unit.realpathCache, path)
	}
}

func (unit *Unit) clearRealpathCache(path string, selective bool) {
	if selective {
		unit.removeRealpathCache(path)
		return
	}
	unit.realpathCache = nil
	unit.realpathCacheSize = 0
	unit.realpathCacheSequence = 0
}

func (unit *Unit) cachedRealpath(path string, now int64) (string, bool) {
	held := unit.realpathCache[path]
	if held == nil {
		return "", false
	}
	if unit.realpathCacheTTL() != 0 && held.expires < now {
		unit.removeRealpathCache(path)
		return "", false
	}
	return held.realpath, true
}

func (unit *Unit) addRealpathCache(path, resolved string, directory bool, now int64) {
	if path == "" || unit.realpathCacheLimit() <= 0 {
		return
	}
	if _, held := unit.realpathCache[path]; held {
		return
	}
	size := realpathCacheBucketSize + int64(len(path)+1)
	if path != resolved {
		size += int64(len(resolved) + 1)
	}
	if unit.realpathCacheSize+size > unit.realpathCacheLimit() {
		return
	}
	expires := int64(-1)
	if ttl := unit.realpathCacheTTL(); ttl != 0 {
		expires = now + ttl
	}
	if unit.realpathCache == nil {
		unit.realpathCache = map[string]*realpathCacheBucket{}
	}
	unit.realpathCacheSequence++
	unit.realpathCache[path] = &realpathCacheBucket{
		path: path, realpath: resolved, key: realpathCacheKey(path),
		expires: expires, isDir: directory,
		sequence: unit.realpathCacheSequence, size: size,
	}
	unit.realpathCacheSize += size
}

func (in *Interp) populateRealpathCache(unit *Unit, requested, resolved string, now int64) {
	addPrefix := func(prefix string) {
		resolvedPrefix, errorNumber, available := libc.RealPath(prefix)
		if !available || errorNumber != 0 {
			return
		}
		information, err := os.Stat(resolvedPrefix)
		if err != nil {
			return
		}
		unit.addRealpathCache(prefix, resolvedPrefix, information.IsDir(), now)
	}
	for _, prefix := range realpathPrefixes(requested) {
		addPrefix(prefix)
	}
	// Dot segments only change spelling. A different cleaned path means a
	// symbolic link redirected traversal; virtual_file_ex also caches the
	// canonical target chain in that case.
	if filepath.Clean(requested) != resolved {
		for _, prefix := range realpathPrefixes(resolved) {
			addPrefix(prefix)
		}
	}
}

func (in *Interp) resolveRealpath(unit *Unit, path string) (string, int, bool) {
	if stdlib.HasStreamScheme(path) {
		return "", 2, true
	}
	requested, err := absoluteRealpathInput(path)
	if err != nil {
		return "", 2, true
	}
	now := time.Now().Unix()
	if resolved, found := unit.cachedRealpath(requested, now); found {
		return resolved, 0, true
	}
	resolved, errorNumber, available := libc.RealPath(requested)
	if !available || errorNumber != 0 {
		return "", errorNumber, available
	}
	in.populateRealpathCache(unit, requested, resolved, now)
	return resolved, 0, true
}

func (unit *Unit) realpathCacheValue() php.Value {
	buckets := make([]*realpathCacheBucket, 0, len(unit.realpathCache))
	for _, bucket := range unit.realpathCache {
		buckets = append(buckets, bucket)
	}
	sort.SliceStable(buckets, func(left, right int) bool {
		leftIndex := buckets[left].key % 1024
		rightIndex := buckets[right].key % 1024
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		return buckets[left].sequence > buckets[right].sequence
	})
	result := php.NewArray()
	for _, bucket := range buckets {
		entry := php.NewArray()
		if bucket.key <= uint64(math.MaxInt64) {
			entry.Set(php.StringKey("key"), php.Int(int64(bucket.key)))
		} else {
			entry.Set(php.StringKey("key"), php.Float(float64(bucket.key)))
		}
		entry.Set(php.StringKey("is_dir"), php.Bool(bucket.isDir))
		entry.Set(php.StringKey("realpath"), php.String(bucket.realpath))
		entry.Set(php.StringKey("expires"), php.Int(bucket.expires))
		result.Set(php.StringKey(bucket.path), php.Arr(entry))
	}
	return php.Arr(result)
}
