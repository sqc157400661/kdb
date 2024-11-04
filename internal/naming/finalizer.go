package naming

const (
	finalizerPrefix = "kdb."
	// Finalizer marks an object to be garbage collected by this module.
	Finalizer = finalizerPrefix + "finalizer"
)
