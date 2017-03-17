build:
	GOOS=linux GOARCH=amd64 go build -o undine_linux
	GOOS=darwin GOARCH=amd64 go build -o undine_darwin
	# GOOS=windows GOARCH=amd64 go build -o undine_windows
	cp undine_darwin ~/.command-pad/undine
