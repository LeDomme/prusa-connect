// Package ref names every instance in the system.
//
// Two identifiers, because an operator renaming an instance must not orphan the
// data rows that reference it: Ref is the operator-chosen name, UID is the
// host-minted identity.
package ref

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// maxIDLen is the DNS-1123 label limit. It is also what keeps an ID inside a
	// single NATS subject token and a URL path segment.
	maxIDLen = 63
	// maxEchoLen bounds how much of a rejected value an error may quote, so an
	// oversized config value cannot become an oversized log line.
	maxEchoLen = 32

	separator     = ":"
	pathSeparator = "/"
	ellipsis      = "..."
)

var (
	// ErrInvalidID reports an ID that is not a DNS-1123 label.
	ErrInvalidID = errors.New("invalid resource id")
	// ErrUnknownKind reports a kind outside the closed set.
	ErrUnknownKind = errors.New("unknown resource kind")
	// ErrMalformed reports a string that is not a rendered Ref.
	ErrMalformed = errors.New("malformed resource ref")
)

// Kind is the closed, core-owned set of resource kinds. There is no
// registration hook: a new kind is a change to this file.
//
// It has two halves. A configurable kind is written through the resource API
// and stored in this server's configuration; an intrinsic kind names something
// the system already knows and no operator declares. Both halves are
// addressable, because the event envelope, the health key and the authorization
// key are one vocabulary and a second set here would drift from it.
//
// Supervised splits the configurable half again. The lifecycle plane constructs
// and runs most configurable kinds. A gcode preset is stored configuration with
// nothing to run, so it carries a resource kind grant and never reaches the
// plane.
type Kind string

const (
	KindStorage Kind = "storage"
	KindAuth    Kind = "auth"
	KindSink    Kind = "sink"
	KindDriver  Kind = "driver"
	KindDB      Kind = "db"
	KindCamera  Kind = "camera"

	// KindHistory is a history provider: the store that keeps metrics and
	// events after the hub has forgotten them. It is separate from KindDB
	// because the two contracts share no method. History is append-only,
	// time-windowed and cursor-paged. A db is per-key and versioned and has no
	// query.
	KindHistory Kind = "history"

	// KindLink is this site's link to one parent site: the extension that
	// carries state up and commands down. A node has one parent, so a
	// deployment holds at most one instance of it.
	KindLink Kind = "link"

	// KindAsset is a module that runs no process: a directory of files the
	// core verifies against a recorded digest and serves to the browser under
	// the module's namespace. Nothing is launched for one.
	KindAsset Kind = "asset"

	// KindGCodePreset is a stored, named gcode sequence. It is configurable and
	// not supervised: it is written through an API of its own into the same
	// machine-owned overlay, under the same version token, and nothing
	// constructs one.
	KindGCodePreset Kind = "gcode-preset"

	// KindDashboard is a page of cards a person lays out. It is stored and
	// not supervised, as a gcode preset is: nothing constructs one, and the
	// org roles reach it through a grant on the kind.
	KindDashboard Kind = "dashboard"

	// KindPrinter and KindNode are intrinsic. A printer is registered by the
	// device plane and carries a fleet-unique identity. A node is a peer site.
	// Neither is declared in the resources block, so Configurable is false for
	// both.
	KindPrinter Kind = "printer"
	KindNode    Kind = "node"
)

// Class is what this server does with instances of a kind. Every kind in the
// closed set has exactly one, and Classify is the only reader of the table, so
// a caller can tell a kind of the wrong class from a kind nobody declared.
//
// Configurable and Supervised answer yes or no, so neither can make that
// distinction: each is false for a kind outside the set and false for printer
// alike. A caller refusing on either one alone has to guess which case it has,
// and a wrong guess tells the author to fix the wrong thing.
type Class string

const (
	// ClassSupervised is configuration the lifecycle plane constructs, starts
	// and reports on.
	ClassSupervised Class = "supervised"
	// ClassStored is configuration with nothing behind it to start. A gcode
	// preset carries a resource kind grant and never reaches the plane.
	ClassStored Class = "stored"
	// ClassIntrinsic is addressable and declared by no operator. A printer is
	// registered by the device plane and a node is a peer site.
	ClassIntrinsic Class = "intrinsic"
	// ClassDevice is a device a person adds through its own API and the
	// lifecycle plane runs. A camera is a row in the host database beside the
	// printers, and no configuration file declares one (ADR-20261116-g5x).
	ClassDevice Class = "device"
)

// classOfKind is the one classification of the closed set. It is a composite
// literal, so the compiler checks it and nothing is assembled at start-up.
// TestEveryKindCarriesOneClass checks that every kind in Kinds has a class.
//
//nolint:gochecknoglobals // a classification table, never written to.
var classOfKind = map[Kind]Class{
	KindStorage:     ClassSupervised,
	KindAuth:        ClassSupervised,
	KindSink:        ClassSupervised,
	KindDriver:      ClassSupervised,
	KindDB:          ClassSupervised,
	KindCamera:      ClassDevice,
	KindHistory:     ClassSupervised,
	KindLink:        ClassSupervised,
	KindAsset:       ClassSupervised,
	KindGCodePreset: ClassStored,
	KindDashboard:   ClassStored,
	KindPrinter:     ClassIntrinsic,
	KindNode:        ClassIntrinsic,
}

// Classify returns what this server does with instances of k, and ErrUnknownKind
// for a kind outside the closed set. A caller that refuses a kind reports the
// reason Classify gives.
func (k Kind) Classify() (Class, error) {
	class, known := classOfKind[k]
	if !known {
		return "", fmt.Errorf("%w: %q is not one of %s", ErrUnknownKind, echo(string(k)), joinKinds())
	}

	return class, nil
}

// Kinds returns the closed set, in declaration order. The configurable kinds
// come first, so appending an intrinsic one cannot reorder the others.
func Kinds() []Kind {
	return []Kind{
		KindStorage, KindAuth, KindSink, KindDriver, KindDB, KindCamera, KindHistory, KindLink, KindAsset,
		KindGCodePreset, KindDashboard, KindPrinter, KindNode,
	}
}

// ConfigurableKinds returns the kinds this server holds configuration for, in
// declaration order. It is the id space of an authorization grant written on a
// resource kind, and nothing outside it compiles to a row.
func ConfigurableKinds() []Kind {
	return []Kind{
		KindStorage, KindAuth, KindSink, KindDriver, KindDB, KindCamera, KindHistory, KindLink, KindAsset,
		KindGCodePreset, KindDashboard,
	}
}

// SupervisedKinds returns the kinds whose instances the resource lifecycle
// plane constructs, starts and reports on, in declaration order. It is the set
// the resources block declares and the set the resource API's kind path accepts.
func SupervisedKinds() []Kind {
	return []Kind{
		KindStorage, KindAuth, KindSink, KindDriver, KindDB, KindHistory, KindLink, KindAsset,
	}
}

// readerOfKind is the supervised kinds this server reads instances of back out
// of the supervision plane, and the ones it does not.
//
// A supervised kind is constructed, started and reported on whether or not
// anything asks it anything. Nothing in the plane can tell the two apart, so the
// answer is written here and internal/arch checks it against the call sites that
// do the reading. A kind that answers false has every declaration of it refused
// at load. An instance that reports ready while nobody asks it anything shows
// an operator a working feature that does nothing.
//
// auth is read by the bearer path in internal/app, which verifies a Prusa
// Account token through the running identity instance. The login flows come
// from core.auth.
//
//nolint:gochecknoglobals,exhaustive // the supervised kinds only; nothing else has an instance to read.
var readerOfKind = map[Kind]bool{
	KindStorage: true,
	KindAuth:    true,
	KindSink:    true,
	KindHistory: true,
	KindDB:      true,
	KindLink:    true,
	// Read by the dial supervisor in internal/app, which opens each session
	// on a driver module's process (ADR-20261123-sat).
	KindDriver: true,
	// Read by the module plane in internal/app, which mounts each verified
	// bundle under its namespace for the HTTP surface.
	KindAsset: true,
}

// soleInstanceKinds are the supervised kinds a deployment holds at most one
// instance of. A site has one parent, so the loader refuses a second link
// block.
//
//nolint:gochecknoglobals,exhaustive // the kinds bounded to one instance; every other kind may repeat.
var soleInstanceKinds = map[Kind]bool{
	KindLink: true,
}

// SoleInstance reports whether this server holds at most one instance of the
// kind. The loader and the resource API both refuse a second declaration
// against it, so the rule is asked here and written nowhere else.
func (k Kind) SoleInstance() bool { return soleInstanceKinds[k] }

// Read reports whether this server reads instances of this kind back out of the
// supervision plane and asks them for the work the kind names.
//
// It answers false for every kind that is not supervised, because there is no
// instance of one to read. A caller refusing a declaration says which of the two
// it has by asking Supervised first.
func (k Kind) Read() bool { return readerOfKind[k] }

// ReadKinds returns the supervised kinds something reads back, in declaration
// order. UnreadKinds returns the rest of the supervised set, which is what a
// declaration is refused against.
func ReadKinds() []Kind { return partitionSupervised(true) }

// UnreadKinds returns the supervised kinds nothing reads back, in declaration
// order. A declaration naming one of them is refused at load.
func UnreadKinds() []Kind { return partitionSupervised(false) }

// partitionSupervised splits the supervised set on Read, keeping declaration
// order so a published list is stable across builds.
func partitionSupervised(read bool) []Kind {
	out := make([]Kind, 0, len(readerOfKind))

	for _, kind := range SupervisedKinds() {
		if kind.Read() == read {
			out = append(out, kind)
		}
	}

	return out
}

// Configurable reports whether this server holds configuration for instances of
// this kind, which is what makes the kind an authorization object.
//
// It answers false for a kind outside the closed set as well as for an
// intrinsic one. A caller that has to tell those apart calls Classify.
func (k Kind) Configurable() bool {
	class, err := k.Classify()

	return err == nil && (class == ClassSupervised || class == ClassStored || class == ClassDevice)
}

// Supervised reports whether the lifecycle plane builds and runs instances of
// this kind. A configurable kind that is not supervised is stored configuration
// with nothing behind it to start.
//
// It answers false for a kind outside the closed set too. Classify separates
// the two.
func (k Kind) Supervised() bool {
	class, err := k.Classify()

	return err == nil && class == ClassSupervised
}

// ValidateID reports whether id is a DNS-1123 label: lowercase alphanumerics
// and '-', starting and ending with an alphanumeric, at most 63 bytes. That
// intersection is what makes one string legal as a Prometheus label value, a
// URL path segment, a config key and a messaging subject token.
//
// It is not sufficient for a filename: Windows reserves con, aux, nul, prn,
// com1..9 and lpt1..9, and every one of them is a valid DNS-1123 label. Code
// that names a file after an ID must add its own prefix.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if len(id) > maxIDLen {
		return fmt.Errorf("%w: %q is %d bytes, the limit is %d", ErrInvalidID, echo(id), len(id), maxIDLen)
	}
	for i := range len(id) {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0 && i < len(id)-1:
		case c == '-':
			return fmt.Errorf("%w: %q starts or ends with '-'", ErrInvalidID, echo(id))
		default:
			return fmt.Errorf(
				"%w: %q contains %q, allowed are lowercase letters, digits and '-'",
				ErrInvalidID, echo(id), string(c),
			)
		}
	}
	return nil
}

// Valid reports whether k is a member of the closed set.
func (k Kind) Valid() bool {
	_, err := k.Classify()

	return err == nil
}

// String returns the kind as written in config, paths and metric labels.
func (k Kind) String() string { return string(k) }

// Ref is the name of one instance. Both fields are validated at construction
// and unexported, so an invalid Ref cannot be written by a struct literal.
type Ref struct {
	kind Kind
	id   string
}

// New validates kind against the closed set and id against DNS-1123.
func New(kind Kind, id string) (Ref, error) {
	if _, err := kind.Classify(); err != nil {
		return Ref{}, err
	}
	if err := ValidateID(id); err != nil {
		return Ref{}, err
	}
	return Ref{kind: kind, id: id}, nil
}

// Parse reads the form String renders, "kind:id".
func Parse(s string) (Ref, error) {
	kind, id, found := strings.Cut(s, separator)
	if !found {
		return Ref{}, fmt.Errorf("%w: %q has no %q separator", ErrMalformed, echo(s), separator)
	}
	return New(Kind(kind), id)
}

// Kind returns the resource kind.
func (r Ref) Kind() Kind { return r.kind }

// ID returns the operator-chosen name.
func (r Ref) ID() string { return r.id }

// IsZero reports whether r was never constructed.
func (r Ref) IsZero() bool { return r == Ref{} }

// String renders "kind:id", the form used by health keys, event subjects and
// authorization keys.
func (r Ref) String() string { return r.kind.String() + separator + r.id }

// Path renders "kind/id", the form used by API paths.
func (r Ref) Path() string { return r.kind.String() + pathSeparator + r.id }

// Compare orders two Refs by kind in the closed set's declaration order, then
// by id. It is the order every listing of instances is written in, and it is
// here so that one order serves all of them.
//
// Ordering kinds alphabetically instead would put db before storage, which is
// not the order anything else in the system lists them in.
func Compare(a, b Ref) int {
	if kinds := slices.Index(Kinds(), a.kind) - slices.Index(Kinds(), b.kind); kinds != 0 {
		return kinds
	}

	return strings.Compare(a.id, b.id)
}

// MarshalText renders the String form, so a Ref survives JSON and YAML despite
// having no exported fields.
func (r Ref) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// UnmarshalText parses the String form.
func (r *Ref) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

func joinKinds() string {
	names := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		names = append(names, k.String())
	}
	return strings.Join(names, ", ")
}

// echo bounds a rejected value before it reaches an error string. The cut is
// pulled back to a rune boundary so the quoted form stays readable.
func echo(s string) string {
	if len(s) <= maxEchoLen {
		return s
	}
	cut := s[:maxEchoLen]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + ellipsis
}

// DeviceKind is what type of machine a registered device is. The verb table is
// keyed by it: a verb exists for a kind or it exists nowhere, and a shelf's
// verb does not exist for a printer.
//
// It is a type of its own and not ref.Kind, because the two sets differ.
// ref.Kind is closed: it is what this server constructs, supervises and holds
// configuration for, and it is a compile-time table. The set of device kinds
// is open, so adding a device class takes no change to the core source. The
// two overlap on the intrinsic kinds a build already carries.
//
// A qualified kind is namespace/name, with the namespace derived by the host
// from the declaring module. A bare name is a core kind.
type DeviceKind string

// The device kinds this build carries in its own right.
const (
	// DeviceKindPrinter is a printer. Callers read the zero DeviceKind as this
	// one, so a device registered with no kind is a printer.
	DeviceKindPrinter DeviceKind = "printer"
	// DeviceKindCamera is a camera. The directory holds it, and it has no verb
	// of its own.
	DeviceKindCamera DeviceKind = "camera"
)

// String returns the kind as it is written in a directory row and in a verb
// table key.
func (k DeviceKind) String() string { return string(k) }

// Or returns k, or fallback when k is empty.
func (k DeviceKind) Or(fallback DeviceKind) DeviceKind {
	if k == "" {
		return fallback
	}

	return k
}
