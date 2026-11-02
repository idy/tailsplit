package tailsplit

// LockSavedState holds the same private-state lock as the CLI engine. An app
// broker keeps this lease while its installed service uses the migrated identity,
// so the legacy CLI cannot use a copy of that identity concurrently.
func LockSavedState(directory string) (func() error, error) {
	if err := privateState(directory); err != nil {
		return nil, err
	}
	return lockState(directory)
}
