package main

// Opening the bundled uninstaller is allowed only after the managed connection
// has finished cleanup. The desktop operation gate excludes concurrent starts.
func removeDesktopNetworkService(stop, openInstaller func() error) error {
	if err := stop(); err != nil {
		return err
	}
	return openInstaller()
}
