package serve

import "errors"

// errNoPeerCred says the system can't tell a socket peer's uid.
var errNoPeerCred = errors.New("peer credentials are not available on this system")
