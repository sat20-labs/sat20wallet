package wallet

func mapContains[T any](a, b map[AssetName]T) bool {
	for k := range b {
		_, ok := a[k]
		if !ok {
			return false
		}
	}
	return true
}

func MapEqual[T any](a, b map[AssetName]T) bool {
	if mapContains(a, b) {
		return mapContains(b, a)
	}
	return false
}
