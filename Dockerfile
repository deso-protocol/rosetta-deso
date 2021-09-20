FROM alpine:edge AS rosetta

RUN apk update
RUN apk upgrade
RUN apk add --update go=1.16.7-r0 gcc g++ vips-dev

WORKDIR /deso/src

COPY deso-rosetta/go.mod deso-rosetta/
COPY deso-rosetta/go.sum deso-rosetta/
COPY core/go.mod core/
COPY core/go.sum core/
COPY core/third_party/ core/third_party/

WORKDIR /deso/src/deso-rosetta

RUN go mod download

# include deso-rosetta src
COPY deso-rosetta/deso          deso
COPY deso-rosetta/cmd           cmd
COPY deso-rosetta/services      services
COPY deso-rosetta/main.go       .

# include core src
COPY core/desohash ../core/desohash
COPY core/cmd       ../core/cmd
COPY core/lib       ../core/lib
COPY core/migrate   ../core/migrate

# build deso-rosetta
RUN GOOS=linux go build -mod=mod -a -installsuffix cgo -o bin/deso-rosetta main.go

# create tiny image
FROM alpine:edge

COPY --from=rosetta /deso/src/deso-rosetta/bin/deso-rosetta /deso/bin/deso-rosetta
