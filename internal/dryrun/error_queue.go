package dryrun

import "sync"

type errorQueue struct {
	mu sync.Mutex // protects fields below

	active bool
	errs   []error
}

var reconcileErrors = &errorQueue{}

func AddError(err error) {
	reconcileErrors.mu.Lock()
	defer reconcileErrors.mu.Unlock()

	if !reconcileErrors.active {
		return
	}

	reconcileErrors.errs = append(reconcileErrors.errs, err)
}

func allErrors() []error {
	reconcileErrors.mu.Lock()
	defer reconcileErrors.mu.Unlock()

	result := make([]error, len(reconcileErrors.errs))
	for _, err := range reconcileErrors.errs {
		result = append(result, err)
	}

	reconcileErrors.errs = nil // clear queue

	return result
}

func enableErrors() {
	reconcileErrors.mu.Lock()
	defer reconcileErrors.mu.Unlock()

	reconcileErrors.active = true
}
