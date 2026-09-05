package windows

import "errors"

// ErrWintunNotLinked is returned until a real Wintun platform binding is provided.
var ErrWintunNotLinked = errors.New("wintun production adapter is not linked in protocol core")
