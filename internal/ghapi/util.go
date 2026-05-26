package ghapi

import (
	"bytes"
	"encoding/json"
	"io"
)

func jsonBody(v any) io.Reader {
	b, err := json.Marshal(v)
	if err != nil {
		// All call sites pass simple structs, so a marshal error is a bug.
		panic(err)
	}
	return bytes.NewReader(b)
}
