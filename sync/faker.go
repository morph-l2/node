package sync

func NewFakeSyncer(db Database) *Syncer {
	return &Syncer{
		db: db,
	}
}
