// Package driverwire is the connector contract on the plugin wire
// (ADR-20261123-sat). A printer driver runs in a module process, and the host
// reaches it through this package as it reaches a builtin driver.
//
// The host calls driver.connect, driver.do and the file calls with a Client.
// The module answers them with a Module, which runs any connectorapi.Outbound.
// The module calls driver.state, driver.content and the other reports back,
// and the host answers them with the same Client.
// Run is the whole main of a driver module.
package driverwire

import (
	"encoding/json"
	"errors"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// The calls the host makes on a driver module.
const (
	// MethodConnect dials one printer and opens a session under the id the
	// host chose.
	MethodConnect wirerpc.Method = "driver.connect"
	// MethodDo runs one command on a session.
	MethodDo wirerpc.Method = "driver.do"
	// MethodStorages lists a session's storages.
	MethodStorages wirerpc.Method = "driver.storages"
	// MethodList lists one directory of a session's files.
	MethodList wirerpc.Method = "driver.list"
	// MethodCreateFolder makes a folder on a session's storage.
	MethodCreateFolder wirerpc.Method = "driver.create_folder"
	// MethodDeleteFile removes a file from a session's storage.
	MethodDeleteFile wirerpc.Method = "driver.delete_file"
	// MethodDeleteFolder removes a folder from a session's storage.
	MethodDeleteFolder wirerpc.Method = "driver.delete_folder"
	// MethodSend writes one file to a session's storage. The module reads the
	// bytes with MethodContent.
	MethodSend wirerpc.Method = "driver.send"
	// MethodClose ends a session.
	MethodClose wirerpc.Method = "driver.close"
)

// The calls a driver module makes on the host. A module names them in its
// manifest's require block with the core- prefix, as core-driver.state.
const (
	// MethodState carries state patches of one session.
	MethodState wirerpc.Method = "driver.state"
	// MethodCapabilities carries a session's new capability set and epoch.
	MethodCapabilities wirerpc.Method = "driver.capabilities"
	// MethodEnded says a session ended by itself.
	MethodEnded wirerpc.Method = "driver.ended"
	// MethodContent reads the bytes of an upload from an offset. The bytes
	// arrive on the call's stream before its answer.
	MethodContent wirerpc.Method = "driver.content"
	// MethodProgress reports how far an upload has gone.
	MethodProgress wirerpc.Method = "driver.progress"
)

// The error codes of this contract.
const (
	// CodeFaulted carries a registered fault in the error's data member.
	CodeFaulted = 4101
	// CodeNoSession answers a call that names a session the peer does not
	// hold.
	CodeNoSession = 4102
	// CodeNotSupported answers a call for a part the session does not have.
	CodeNotSupported = 4103
)

// The limits of this contract. The receiving side checks them.
const (
	// MaxPatches is how many patches one MethodState call carries.
	MaxPatches = 256
	// MaxPathBytes is the longest storage or state path a call carries.
	MaxPathBytes = 1024
)

// The refusals of this contract.
var (
	// ErrNoSession reports a call for a session that is not held.
	ErrNoSession = errors.New("driverwire: no such session")
	// ErrNotSupported reports a call for a part the session does not have.
	ErrNotSupported = errors.New("driverwire: the session does not have that part")
	// ErrNoDriver reports a call made before the module has its settings.
	ErrNoDriver = errors.New("driverwire: the module has no driver yet")
	// ErrTooMany reports a value over one of this contract's limits.
	ErrTooMany = errors.New("driverwire: a value is over the contract's limit")
)

// SessionID names one session inside one module process.
type SessionID uint64

// Parts are the optional parts of the contract one session has.
type Parts struct {
	Files     bool `json:"files,omitempty"`
	Manager   bool `json:"manager,omitempty"`
	Transfers bool `json:"transfers,omitempty"`
}

// ConnectParams are the params of MethodConnect. The host picks the session
// id, so it holds the session before the module's first report can arrive. A
// nil profile is the driver's own baseline.
type ConnectParams struct {
	Session  SessionID             `json:"session"`
	Endpoint connectorapi.Endpoint `json:"endpoint"`
	Profile  connectorapi.Set      `json:"profile"`
}

// Connected is the answer to MethodConnect.
type Connected struct {
	Identity     connectorapi.Identity `json:"identity"`
	Capabilities connectorapi.Set      `json:"capabilities"`
	Epoch        uint64                `json:"epoch"`
	Parts        Parts                 `json:"parts"`
}

// SessionParams name a session, for a call that takes nothing else.
type SessionParams struct {
	Session SessionID `json:"session"`
}

// DoParams are the params of MethodDo.
type DoParams struct {
	Session SessionID            `json:"session"`
	Command connectorapi.Command `json:"command"`
}

// PathParams are the params of the calls that name one path.
type PathParams struct {
	Session SessionID `json:"session"`
	Path    string    `json:"path"`
}

// Storages is the answer to MethodStorages.
type Storages struct {
	Storages []connectorapi.Storage `json:"storages"`
}

// SendParams are the params of MethodSend. Upload names the upload in the
// MethodContent and MethodProgress calls the module makes while it runs.
type SendParams struct {
	Session SessionID `json:"session"`
	Upload  uint64    `json:"upload"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
}

// StateParams are the params of MethodState.
type StateParams struct {
	Session SessionID                 `json:"session"`
	Patches []connectorapi.StatePatch `json:"patches"`
}

// CapabilitiesParams are the params of MethodCapabilities.
type CapabilitiesParams struct {
	Session      SessionID        `json:"session"`
	Capabilities connectorapi.Set `json:"capabilities"`
	Epoch        uint64           `json:"epoch"`
}

// EndedParams are the params of MethodEnded.
type EndedParams struct {
	Session SessionID `json:"session"`
	Reason  string    `json:"reason,omitempty"`
}

// ContentParams are the params of MethodContent.
type ContentParams struct {
	Upload uint64 `json:"upload"`
	Offset int64  `json:"offset"`
}

// ProgressParams are the params of MethodProgress.
type ProgressParams struct {
	Upload   uint64                `json:"upload"`
	Progress connectorapi.Progress `json:"progress"`
}

// decode reads the params of one call.
func decode[T any](call wirerpc.Call) (T, error) {
	var params T

	if err := json.Unmarshal(call.Params, &params); err != nil {
		return params, &wirerpc.Error{Code: wirerpc.CodeInvalidParams, Message: err.Error()}
	}

	return params, nil
}
