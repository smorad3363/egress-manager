package xray

// legacyCandidateCatalog is retained only so the legacy adapter tests can continue to
// exercise their parser and safety helpers. Production discovery is intentionally
// disabled: Egress Manager owns its own Xray runtime and must not inspect other panels.
var legacyCandidateCatalog = append([]candidate(nil), knownCandidates...)

func init() {
	knownCandidates = nil
}
