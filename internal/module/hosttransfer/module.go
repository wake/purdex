package hosttransfer

// Module serves host transfer codes over /api/host-transfer*.
type Module struct {
	store *Store
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{} }
