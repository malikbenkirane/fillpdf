#!/bin/sh -e
docker run --rm -it \
  -v $GOPATH:/go \
  -v $PWD:/app \
  -e 'GOPATH=/go' \
  -w /app \
  golang sh -c 'CGO_ENABLED=0 go build -a --installsuffix cgo --ldflags="-s" -o fillpdf'

docker build . -t muse/magicregistrar
