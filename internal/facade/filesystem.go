// Package facade mounts the least-privilege workspace view.
//
// A Facade is a FUSE filesystem over a plain directory (the workspace).
// Policy-allowed paths pass through transparently; every other path is
// refused with EACCES before any byte reaches the run. Denied paths do
// not appear in directory listings, and their metadata is reduced to a
// type-only placeholder so even stat cannot leak content shape.
//
// Refused reads are the grant boundary: the facade asks the Broker to
// approve just-in-time read access for the rest of the run. Grants only
// ever cover reading - a write to a sensitive path is never grantable.
//
// The facade is an enforcement layer for the workspace view, not a
// sandbox boundary: a privileged process that reaches the backing
// directory directly is outside its reach.
package facade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/SuperMarioYL/readscope/internal/grant"
	"github.com/SuperMarioYL/readscope/internal/manifest"
	"github.com/SuperMarioYL/readscope/internal/policy"
)

// Broker approves just-in-time read grants on denied paths. It is
// implemented by the runner's inline terminal approval flow.
type Broker interface {
	// RequestGrant asks the operator to approve read access to rel.
	// It returns true when an active grant now covers rel.
	RequestGrant(rel string) bool
}

// Options configures one facade.
type Options struct {
	// Source is the backing workspace directory (the real files).
	Source string
	// Policy decides which paths pass through by default.
	Policy *policy.Policy
	// Grants holds the run's approved read grants.
	Grants *grant.Store
	// Broker, when set, is asked on every denied read (open or
	// directory listing) so the run can request a grant.
	Broker Broker
	// Log receives every decision in real time (decisions.jsonl).
	Log *manifest.Log
	// Now overrides the clock (tests).
	Now func() time.Time
}

// Facade is the root of the default-deny workspace view. Mount it with
// Mount and run agents with the mountpoint as their working directory.
type Facade struct {
	fs.LoopbackRoot

	opts Options
}

// node is every inode of the facade, including the root. It embeds the
// loopback passthrough and intercepts the operations that expose
// content or change it.
type node struct {
	*fs.LoopbackNode
	f *Facade
}

// New validates options and builds the facade root.
func New(opts Options) (*Facade, error) {
	if opts.Policy == nil {
		return nil, fmt.Errorf("facade: policy is required")
	}
	if opts.Grants == nil {
		return nil, fmt.Errorf("facade: grant store is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	src, err := filepath.Abs(opts.Source)
	if err != nil {
		return nil, fmt.Errorf("facade source: %w", err)
	}
	var st syscall.Stat_t
	if err := syscall.Stat(src, &st); err != nil {
		return nil, fmt.Errorf("facade source %s: %w", src, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return nil, fmt.Errorf("facade source %s is not a directory", src)
	}

	f := &Facade{opts: opts}
	f.LoopbackRoot.Path = src
	f.LoopbackRoot.Dev = uint64(st.Dev)
	f.LoopbackRoot.NewNode = func(rootData *fs.LoopbackRoot, parent *fs.Inode, name string, st *syscall.Stat_t) fs.InodeEmbedder {
		return &node{f: f, LoopbackNode: &fs.LoopbackNode{RootData: rootData}}
	}
	root := &node{f: f, LoopbackNode: &fs.LoopbackNode{RootData: &f.LoopbackRoot}}
	f.LoopbackRoot.RootNode = root
	return f, nil
}

// Mount mounts the facade at mountpoint and starts serving. FUSE
// request logging is enabled when READSCOPE_DEBUG is set.
func (f *Facade) Mount(mountpoint string) (*fuse.Server, error) {
	noTimeout := time.Duration(0)
	server, err := fs.Mount(mountpoint, f.RootNode, &fs.Options{
		MountOptions: fuse.MountOptions{
			Options: []string{"fsname=readscope", "subtype=readscope"},
			Debug:   os.Getenv("READSCOPE_DEBUG") != "",
		},
		EntryTimeout: &noTimeout,
		AttrTimeout:  &noTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("mount facade at %s: %w", mountpoint, err)
	}
	return server, nil
}

// Source returns the backing workspace path.
func (f *Facade) Source() string { return f.LoopbackRoot.Path }

// ---- decision helpers ----

// relPath is the workspace-relative path of the node's inode. It is
// recomputed on every use so renamed nodes stay correct.
func (n *node) relPath() string {
	return n.EmbeddedInode().Path(nil)
}

// underlyingPath is the backing-file path for the node's child name.
func (n *node) underlyingPath(name string) string {
	return filepath.Join(n.f.LoopbackRoot.Path, n.relPath(), name)
}

// visible reports whether a path may be seen or read right now (policy
// allow or an active grant). It never triggers the broker.
func (f *Facade) visible(rel string) bool {
	if rel == "" {
		return true
	}
	if f.opts.Policy.Decide(rel, policy.Read) == policy.Allow {
		return true
	}
	_, ok := f.opts.Grants.Check(rel, f.opts.Now())
	return ok
}

func (f *Facade) now() time.Time { return f.opts.Now() }

func (f *Facade) log(op, verb, rel, verdict string) {
	if f.opts.Log == nil {
		return
	}
	_ = f.opts.Log.Append(manifest.Decision{
		At:      f.now(),
		Op:      op,
		Verb:    verb,
		Path:    rel,
		Verdict: verdict,
	})
}

// requestGrant consults the broker when a read is denied and reports
// whether the path became readable.
func (f *Facade) requestGrant(rel string) bool {
	if f.opts.Broker == nil {
		return false
	}
	if !f.opts.Broker.RequestGrant(rel) {
		return false
	}
	_, ok := f.opts.Grants.Check(rel, f.now())
	return ok
}

// ---- lookup and metadata ----

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	var st syscall.Stat_t
	if err := syscall.Lstat(n.underlyingPath(name), &st); err != nil {
		return nil, fs.ToErrno(err)
	}
	child := n.f.newChild(n, &st, ctx)

	rel := n.f.joinRel(n.relPath(), name)
	if n.f.visible(rel) {
		out.Attr.FromStat(&st)
	} else {
		// Type stays correct (dirs must look like dirs) but size,
		// ownership and timestamps are withheld, and no caching so
		// the next stat re-evaluates policy and grants.
		out.Attr = typeOnlyAttr(&st)
		out.SetEntryTimeout(0)
		out.SetAttrTimeout(0)
	}
	return child, 0
}

func (f *Facade) newChild(parent *node, st *syscall.Stat_t, ctx context.Context) *fs.Inode {
	child := &node{f: f, LoopbackNode: &fs.LoopbackNode{RootData: &f.LoopbackRoot}}
	return parent.NewInode(ctx, child, fs.StableAttr{
		Mode: uint32(st.Mode),
		Gen:  1,
		Ino:  (uint64(st.Dev) << 32) | (uint64(st.Ino) & 0xffffffff),
	})
}

// typeOnlyAttr keeps the file type but hides everything a policy-denied
// path could reveal about itself.
func typeOnlyAttr(st *syscall.Stat_t) fuse.Attr {
	return fuse.Attr{
		Mode:    uint32(st.Mode) & syscall.S_IFMT,
		Nlink:   1,
		Blksize: 4096,
	}
}

func (n *node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if rel := n.relPath(); rel != "" && !n.f.visible(rel) {
		n.f.log("stat", "read", rel, manifest.VerdictDenied)
		return syscall.EACCES
	}
	return n.LoopbackNode.Getattr(ctx, f, out)
}

// ---- content access: the grant boundary ----

// openIntent classifies an open flags word.
func openIntent(flags uint32) (wantRead, wantWrite bool) {
	switch flags & syscall.O_ACCMODE {
	case syscall.O_RDONLY:
		return true, false
	case syscall.O_WRONLY:
		return false, true
	default: // O_RDWR
		return true, true
	}
}

// openRel applies policy and grants to one open request. It returns
// non-zero errno to refuse.
func (n *node) openRel(rel string, verb string) syscall.Errno {
	if verb == "write" && n.f.opts.Policy.Decide(rel, policy.Write) != policy.Allow {
		n.f.log("open", "write", rel, manifest.VerdictDenied)
		return syscall.EACCES
	}
	if verb == "read" {
		if n.f.opts.Policy.Decide(rel, policy.Read) == policy.Allow {
			n.f.log("open", "read", rel, manifest.VerdictAllowed)
			return 0
		}
		if _, ok := n.f.opts.Grants.Check(rel, n.f.now()); ok {
			n.f.log("open", "read", rel, manifest.VerdictGranted)
			return 0
		}
		if n.f.requestGrant(rel) {
			n.f.log("open", "read", rel, manifest.VerdictGranted)
			return 0
		}
		n.f.log("open", "read", rel, manifest.VerdictDenied)
		return syscall.EACCES
	}
	return 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	rel := n.relPath()
	if rel != "" {
		wantRead, wantWrite := openIntent(flags)
		if wantWrite {
			if errno := n.openRel(rel, "write"); errno != 0 {
				return nil, 0, errno
			}
		}
		if wantRead {
			if errno := n.openRel(rel, "read"); errno != 0 {
				return nil, 0, errno
			}
		}
	}
	return n.LoopbackNode.Open(ctx, flags)
}

// OpendirHandle handles directory opens; listing a denied directory is
// a read and can be granted just like a file read.
func (n *node) OpendirHandle(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	rel := n.relPath()
	if rel != "" {
		if errno := n.openRel(rel, "read"); errno != 0 {
			return nil, 0, errno
		}
		n.f.log("list", "read", rel, manifest.VerdictAllowed)
	}
	ds, errno := fs.NewLoopbackDirStream(n.f.underlyingOf(rel))
	if errno != 0 {
		return nil, 0, errno
	}
	return &filteredDir{f: n.f, dirRel: rel, ds: ds}, 0, 0
}

// Readdir backs the stream interface used when no opendir handle is in
// play; keep it filtered for parity.
func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	rel := n.relPath()
	ds, errno := fs.NewLoopbackDirStream(n.f.underlyingOf(rel))
	if errno != 0 {
		return nil, errno
	}
	return &filteredDir{f: n.f, dirRel: rel, ds: ds}, 0
}

func (f *Facade) underlyingOf(rel string) string {
	if rel == "" {
		return f.LoopbackRoot.Path
	}
	return filepath.Join(f.LoopbackRoot.Path, filepath.FromSlash(rel))
}

func (f *Facade) joinRel(parent, name string) string {
	if parent == "" {
		return f.normalizeRel(name)
	}
	return f.normalizeRel(parent + "/" + name)
}

func (f *Facade) normalizeRel(p string) string {
	out, ok := policy.Normalize(p)
	if !ok {
		return "\x00escaped"
	}
	return out
}

// filteredDir hides policy-denied entries from directory listings so
// sensitive files are not even enumerated inside the view.
type filteredDir struct {
	f      *Facade
	dirRel string
	ds     fs.DirStream
	buf    *fuse.DirEntry
}

var (
	_ fs.DirStream         = (*filteredDir)(nil)
	_ fs.FileReaddirenter  = (*filteredDir)(nil)
	_ fs.FileReleasedirer  = (*filteredDir)(nil)
	_ fs.FileSeekdirer     = (*filteredDir)(nil)
)

func (d *filteredDir) Readdirent(ctx context.Context) (*fuse.DirEntry, syscall.Errno) {
	for {
		if d.buf != nil {
			de := *d.buf
			d.buf = nil
			return &de, 0
		}
		if !d.ds.HasNext() {
			return nil, 0
		}
		de, errno := d.ds.Next()
		if errno != 0 {
			return nil, errno
		}
		rel := d.f.joinRel(d.dirRel, de.Name)
		if rel != "\x00escaped" && d.f.visible(rel) {
			entry := de
			d.buf = &entry
		}
	}
}

func (d *filteredDir) HasNext() bool {
	return d.buf != nil || d.ds.HasNext()
}

func (d *filteredDir) Next() (fuse.DirEntry, syscall.Errno) {
	de, errno := d.Readdirent(context.Background())
	if de == nil {
		return fuse.DirEntry{}, errno
	}
	return *de, errno
}

func (d *filteredDir) Close() {
	d.ds.Close()
}

func (d *filteredDir) Releasedir(ctx context.Context, flags uint32) {
	d.ds.Close()
}

func (d *filteredDir) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	d.buf = nil
	if sd, ok := d.ds.(fs.FileSeekdirer); ok {
		return sd.Seekdir(ctx, off)
	}
	return syscall.ENOTSUP
}

// ---- content reads outside open ----

func (n *node) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	if rel := n.relPath(); rel != "" && !n.f.visible(rel) {
		n.f.log("stat", "read", rel, manifest.VerdictDenied)
		return nil, syscall.EACCES
	}
	return n.LoopbackNode.Readlink(ctx)
}

func (n *node) Getxattr(ctx context.Context, attr string, dest []byte) (uint32, syscall.Errno) {
	if rel := n.relPath(); rel != "" && !n.f.visible(rel) {
		return 0, syscall.EACCES
	}
	return n.LoopbackNode.Getxattr(ctx, attr, dest)
}

// ---- mutations: grants never cover writes ----

// checkWrite admits a namespace mutation on parent/name only when the
// target path is policy-writable.
func (n *node) checkWrite(name string) syscall.Errno {
	target := n.f.joinRel(n.relPath(), name)
	if target == "\x00escaped" || n.f.opts.Policy.Decide(target, policy.Write) != policy.Allow {
		if target != "\x00escaped" {
			n.f.log("write", "write", target, manifest.VerdictDenied)
		}
		return syscall.EACCES
	}
	return 0
}

func (n *node) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if rel := n.relPath(); rel != "" && n.f.opts.Policy.Decide(rel, policy.Write) != policy.Allow {
		n.f.log("write", "write", rel, manifest.VerdictDenied)
		return syscall.EACCES
	}
	return n.LoopbackNode.Setattr(ctx, f, in, out)
}

func (n *node) Setxattr(ctx context.Context, attr string, data []byte, flags uint32) syscall.Errno {
	if rel := n.relPath(); rel != "" && n.f.opts.Policy.Decide(rel, policy.Write) != policy.Allow {
		return syscall.EACCES
	}
	return n.LoopbackNode.Setxattr(ctx, attr, data, flags)
}

func (n *node) Removexattr(ctx context.Context, attr string) syscall.Errno {
	if rel := n.relPath(); rel != "" && n.f.opts.Policy.Decide(rel, policy.Write) != policy.Allow {
		return syscall.EACCES
	}
	return n.LoopbackNode.Removexattr(ctx, attr)
}

func (n *node) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.checkWrite(name); errno != 0 {
		return nil, errno
	}
	return n.LoopbackNode.Mkdir(ctx, name, mode, out)
}

func (n *node) Mknod(ctx context.Context, name string, mode, rdev uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.checkWrite(name); errno != 0 {
		return nil, errno
	}
	return n.LoopbackNode.Mknod(ctx, name, mode, rdev, out)
}

func (n *node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if errno := n.checkWrite(name); errno != 0 {
		return nil, nil, 0, errno
	}
	return n.LoopbackNode.Create(ctx, name, flags, mode, out)
}

func (n *node) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.checkWrite(name); errno != 0 {
		return nil, errno
	}
	return n.LoopbackNode.Symlink(ctx, target, name, out)
}

func (n *node) Link(ctx context.Context, target fs.InodeEmbedder, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if errno := n.checkWrite(name); errno != 0 {
		return nil, errno
	}
	return n.LoopbackNode.Link(ctx, target, name, out)
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	if errno := n.checkWrite(name); errno != 0 {
		return errno
	}
	return n.LoopbackNode.Unlink(ctx, name)
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	if errno := n.checkWrite(name); errno != 0 {
		return errno
	}
	return n.LoopbackNode.Rmdir(ctx, name)
}

func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if errno := n.checkWrite(name); errno != 0 {
		return errno
	}
	dstParent, ok := newParent.(*node)
	if !ok {
		return syscall.EACCES
	}
	dest := dstParent.f.joinRel(dstParent.relPath(), newName)
	if dest == "\x00escaped" || dstParent.f.opts.Policy.Decide(dest, policy.Write) != policy.Allow {
		dstParent.f.log("write", "write", dest, manifest.VerdictDenied)
		return syscall.EACCES
	}
	return n.LoopbackNode.Rename(ctx, name, newParent, newName, flags)
}

// sanity: the node must keep satisfying go-fuse's embedder contract.
var _ fs.InodeEmbedder = (*node)(nil)
