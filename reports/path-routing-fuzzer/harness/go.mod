module prf/harness

go 1.27.1

require (
	github.com/FlavioCFOliveira/MuxMaster v0.0.0-20260416175757-434f76cb080e
	github.com/go-chi/chi/v5 v5.3.2
	github.com/julienschmidt/httprouter v1.3.0
	github.com/uptrace/bunrouter v1.0.23
)

require github.com/stretchr/testify v1.12.1 // indirect

replace github.com/FlavioCFOliveira/MuxMaster => ../../../
