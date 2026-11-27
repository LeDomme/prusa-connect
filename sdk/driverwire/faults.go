package driverwire

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// wireError is what one side's handler answers for err. A registered fault
// travels whole, so the host reports the code the driver chose. Any other
// error keeps its sentence, which the host logs.
func wireError(err error) error {
	if err == nil {
		return nil
	}

	if coded, ok := errors.AsType[*wirerpc.Error](err); ok {
		return coded
	}

	if raised, ok := errors.AsType[*fault.Fault](err); ok {
		data, encodeErr := json.Marshal(raised.Payload())
		if encodeErr == nil {
			return &wirerpc.Error{Code: CodeFaulted, Message: raised.Error(), Data: data}
		}
	}

	switch {
	case errors.Is(err, ErrNoSession):
		return &wirerpc.Error{Code: CodeNoSession, Message: err.Error()}
	case errors.Is(err, ErrNotSupported):
		return &wirerpc.Error{Code: CodeNotSupported, Message: err.Error()}
	}

	return &wirerpc.Error{Code: wirerpc.CodeInternal, Message: err.Error()}
}

// localError turns what the peer answered back into an error of this side: the
// fault it carried, one of this contract's variables, or the peer's sentence.
func localError(method wirerpc.Method, err error) error {
	coded, ok := errors.AsType[*wirerpc.Error](err)
	if !ok {
		return fmt.Errorf("driverwire: %s: %w", method, err)
	}

	switch coded.Code {
	case CodeFaulted:
		var payload fault.Payload
		if json.Unmarshal(coded.Data, &payload) == nil && !payload.Code.IsZero() {
			return fault.New(payload.Code, payload.Detail).With(payload.Params...)
		}
	case CodeNoSession:
		return fmt.Errorf("driverwire: %s: %w", method, ErrNoSession)
	case CodeNotSupported:
		return fmt.Errorf("driverwire: %s: %w", method, ErrNotSupported)
	}

	return fmt.Errorf("driverwire: %s: %s", method, coded.Message) //nolint:err113 // the peer's own sentence
}
