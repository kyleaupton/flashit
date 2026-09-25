package validate

func hostDevicePath(path string) (string, error) {
	p, _, err := PhysicalDrivePath(path)
	return p, err
}
