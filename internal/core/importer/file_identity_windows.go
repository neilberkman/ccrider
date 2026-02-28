//go:build windows

package importer

func getFileIdentity(_ string) (uint64, uint64, error) {
	return 0, 0, nil
}
