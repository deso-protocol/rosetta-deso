FROM alpine:latest AS rosetta

RUN apk update && apk upgrade && apk add --update go gcc g++ vips-dev

WORKDIR /bitclout/src

COPY rosetta-deso/go.mod rosetta-bitclout/
COPY rosetta-deso/go.sum rosetta-bitclout/
COPY core/go.mod core/
COPY core/go.sum core/
COPY core/third_party/ core/third_party/

WORKDIR /bitclout/src/rosetta-bitclout

RUN go mod download

# include rosetta-bitclout src
COPY rosetta-deso/bitclout      bitclout
COPY rosetta-deso/cmd           cmd
COPY rosetta-deso/services      services
COPY rosetta-deso/main.go       .

# include core src
COPY core/clouthash ../core/clouthash
COPY core/cmd       ../core/cmd
COPY core/lib       ../core/lib
COPY core/migrate   ../core/migrate

# build rosetta-bitclout
RUN GOOS=linux go build -mod=mod -a -installsuffix cgo -o bin/rosetta-bitclout main.go

# create tiny image
FROM alpine:edge

COPY --from=rosetta /bitclout/src/rosetta-bitclout/bin/rosetta-bitclout /bitclout/bin/rosetta-bitclout
