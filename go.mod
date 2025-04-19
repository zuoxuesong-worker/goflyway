module github.com/coyove/goflyway

go 1.23.0

toolchain go1.23.8

require (
	github.com/coyove/common v0.0.0-20240403014525-f70e643f9de8
	github.com/miekg/dns v1.1.65
	golang.org/x/crypto v0.37.0
)

require (
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/net v0.39.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
)

replace github.com/coyove/common => ./staging/src/github.com/coyove/common
