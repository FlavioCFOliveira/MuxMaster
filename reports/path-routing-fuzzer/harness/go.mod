module github.com/FlavioCFOliveira/MuxMaster/reports/path-routing-fuzzer/harness

go 1.26

replace github.com/FlavioCFOliveira/MuxMaster => ../../..

require (
	github.com/FlavioCFOliveira/MuxMaster v0.0.0-00010101000000-000000000000
	github.com/go-chi/chi/v5 v5.2.5
	github.com/julienschmidt/httprouter v1.3.0
	github.com/uptrace/bunrouter v1.0.23
)
