FROM alpine:latest AS rosetta

RUN apk update && apk upgrade && apk add --update go gcc g++ vips-dev

COPY --from=golang:1.24-alpine /usr/local/go/ /usr/local/go/
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /deso/src

COPY rosetta-deso/go.mod rosetta-deso/
COPY rosetta-deso/go.sum rosetta-deso/
COPY core/go.mod core/
COPY core/go.sum core/

WORKDIR /deso/src/rosetta-deso

RUN go mod download

# include rosetta-deso src
COPY rosetta-deso/deso          deso
COPY rosetta-deso/cmd           cmd
COPY rosetta-deso/services      services
COPY rosetta-deso/main.go       .

# include core src
COPY core/desohash    ../core/desohash
COPY core/cmd         ../core/cmd
COPY core/lib         ../core/lib
COPY core/migrate     ../core/migrate
COPY core/bls         ../core/bls
COPY core/collections ../core/collections
COPY core/consensus   ../core/consensus

# build rosetta-deso
RUN GOOS=linux go build -mod=mod -a -installsuffix cgo -o bin/rosetta-deso main.go

# create tiny image
FROM alpine:edge

COPY --from=rosetta /deso/src/rosetta-deso/bin/rosetta-deso /deso/bin/rosetta-deso
