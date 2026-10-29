package hosted

import "fmt"

// NotHolderError is the server's 403 to a substrate publish by a caller who is
// not the project's substrate holder; its body names the holder. It is the
// project config route's refusal, not the workspace catalog's ErrNotPublisher.
type NotHolderError struct {
	Path   string
	Holder SubstrateHolder
}

func (e *NotHolderError) Error() string {
	return fmt.Sprintf("PUT config %s: only the project's substrate holder %s can publish it", e.Path, e.Holder.Email)
}
