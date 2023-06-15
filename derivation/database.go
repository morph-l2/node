package derivation

type Database interface {
	Reader
	Writer
}

type Reader interface {
	ReadLatestDerivationL1Height() *uint64
}

type Writer interface {
	WriteLatestDerivationL1Height(latest uint64)
}
