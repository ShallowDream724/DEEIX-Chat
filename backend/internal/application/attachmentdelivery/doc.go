// Package attachmentdelivery provides repeatable, short lived HTTPS capabilities
// for model attachment input. The cache holds only SHA-256 token digests and
// trusted source metadata. Each request rechecks owner, active file state and
// source SHA/size/storage key; prepared copies retain their original file binding.
//
// Original objects are opened from objectstorage without copying or materializing.
// Prepared images and authorized temporary session readers are copied once into
// StorageRootDir/attachment-delivery, with a 100 MiB per-file limit, at most 100
// files and a conservative 1 GiB disk budget per service instance. The directory
// is private and is never registered as an HTTP static root. Deployments serving
// one cache across several instances must share this directory, as with local
// temporary session attachments. Files encode their absolute expiry, allowing
// startup and minute-based cleanup even after cache eviction or a restart.
//
// The default and maximum grant lifetime is three hours, covering the current
// two-hour task allowance plus bounded fetch grace. Callers use a unique run ID
// and ReleaseRun when work ends or is canceled. Revocation stops subsequent
// GET/HEAD requests immediately; already transmitted bytes cannot be recalled.
package attachmentdelivery
