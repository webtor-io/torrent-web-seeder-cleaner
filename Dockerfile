FROM golang:latest as build

# set work dir
WORKDIR /app

# copy the source files
COPY . .

# disable crosscompiling
ENV CGO_ENABLED=0

# compile linux only
ENV GOOS=linux

# build the binary with debug information removed
RUN  go build -ldflags '-w -s' -a -installsuffix cgo -o server

FROM alpine:latest

# copy our static linked library
COPY --from=build /app/server .

# tell we are exposing our service on port 8081
EXPOSE 8081

# run it!
CMD ["./server", "serve"]
