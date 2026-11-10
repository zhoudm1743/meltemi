package gl

import (
	"container/list"

	gogl "github.com/go-gl/gl/v3.3-core/gl"

	"github.com/zhoudm1743/meltemi/internal/text"
)

// maxTextCacheTextures bounds the coverage textures cached per renderer. The
// cache only retains mask textures, so the bound is a bound on texture memory
// too; least-recently-used entries are deleted past it.
const maxTextCacheTextures = 256

// textCacheKey identifies one coverage texture: the face, the text and the
// physical rasterization scales Begin read from the target. Color and opacity
// are deliberately absent: they are applied per draw through the text
// program's uniform, so one coverage texture serves every tint of the same
// text.
type textCacheKey struct {
	face   *text.Face
	value  string
	scaleX float64
	scaleY float64
}

// textCacheEntry is one cached coverage texture plus the logical geometry of
// the bitmap it was uploaded from. The alpha bytes are not retained: after
// the upload only the GL texture carries the mask.
type textCacheEntry struct {
	tex uint32

	logicalWidth  float64
	logicalHeight float64

	// item is this entry's position in the cache's LRU list.
	item *list.Element
}

// textCache is a bounded LRU of coverage textures belonging to one Renderer
// and therefore one native GL context. Texture names are never placed in
// another renderer's cache. Like textStates it is only touched on the UI
// thread, so it needs no synchronization.
type textCache struct {
	entries map[textCacheKey]*textCacheEntry
	lru     *list.List // front = most recently used, values are textCacheKey

	hits      uint64
	misses    uint64
	uploads   uint64
	evictions uint64
}

// textCaches holds the per-renderer coverage caches, keyed by renderer so a
// renderer, its window and its textures are released together.
var textCaches = map[*Renderer]*textCache{}

// textCacheFor returns the cache owned by r, creating an empty one on first
// use.
func textCacheFor(r *Renderer) *textCache {
	if cache, ok := textCaches[r]; ok {
		return cache
	}
	cache := &textCache{
		entries: map[textCacheKey]*textCacheEntry{},
		lru:     list.New(),
	}
	textCaches[r] = cache
	return cache
}

// lookup returns the entry for key, counting a hit and refreshing its LRU
// position, or counts a miss and returns nil.
func (c *textCache) lookup(key textCacheKey) (*textCacheEntry, bool) {
	entry, ok := c.entries[key]
	if !ok {
		c.misses++
		return nil, false
	}
	c.hits++
	c.lru.MoveToFront(entry.item)
	return entry, true
}

// upload stores bmp's alpha as a new coverage texture under key, evicting the
// least-recently-used entry first when the cache is full. The caller owns the
// owning context, which DrawText guarantees: rasterization, upload and
// eviction all run between Begin and End with the target current.
func (c *textCache) upload(key textCacheKey, bmp text.Bitmap) uint32 {
	if len(c.entries) >= maxTextCacheTextures {
		c.evictOldest()
	}

	var texture uint32
	gogl.GenTextures(1, &texture)
	gogl.ActiveTexture(gogl.TEXTURE0)
	gogl.BindTexture(gogl.TEXTURE_2D, texture)
	// Rows are Stride bytes apart and only one byte per pixel, so a row may
	// start at any byte offset; the default alignment of 4 would corrupt
	// widths that are not multiples of 4. Width and Height are the physical
	// mask size; the quad uses the logical size instead.
	gogl.PixelStorei(gogl.UNPACK_ALIGNMENT, 1)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_MIN_FILTER, gogl.LINEAR)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_MAG_FILTER, gogl.LINEAR)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_WRAP_S, gogl.CLAMP_TO_EDGE)
	gogl.TexParameteri(gogl.TEXTURE_2D, gogl.TEXTURE_WRAP_T, gogl.CLAMP_TO_EDGE)
	gogl.TexImage2D(gogl.TEXTURE_2D, 0, gogl.R8, int32(bmp.Width), int32(bmp.Height), 0, gogl.RED, gogl.UNSIGNED_BYTE, gogl.Ptr(bmp.Alpha))

	entry := &textCacheEntry{
		tex:           texture,
		logicalWidth:  bmp.LogicalWidth,
		logicalHeight: bmp.LogicalHeight,
	}
	entry.item = c.lru.PushFront(key)
	c.entries[key] = entry
	c.uploads++
	return texture
}

// evictOldest deletes the least-recently-used coverage texture and drops its
// entry. It runs inside DrawText with the owning context current.
func (c *textCache) evictOldest() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	key := back.Value.(textCacheKey)
	entry := c.entries[key]
	delete(c.entries, key)
	c.lru.Remove(back)
	c.evictions++
	if entry.tex != 0 {
		gogl.DeleteTextures(1, &entry.tex)
	}
}

// releaseTextCache deletes every coverage texture owned by r and drops the
// cache entry so the Renderer and its window can be collected. The caller is
// Renderer.Close, which runs on the UI thread with r's own context current.
func releaseTextCache(r *Renderer) {
	cache, ok := textCaches[r]
	if !ok {
		return
	}
	delete(textCaches, r)
	for _, entry := range cache.entries {
		if entry.tex != 0 {
			gogl.DeleteTextures(1, &entry.tex)
		}
	}
	cache.entries = nil
	cache.lru = nil
}

// discardTextCache drops only the Go references cached for r. Native window
// destruction reclaims the GL texture names; making no GL calls is what keeps
// Discard safe without a current context.
func discardTextCache(r *Renderer) {
	delete(textCaches, r)
}
