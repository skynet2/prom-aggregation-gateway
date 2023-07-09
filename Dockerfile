# builder image
FROM golang:alpine as builder

ARG COMMIT_SHA
ARG VERSION_TAG
ARG GO_MOD_PATH="github.com/zapier/prom-aggregation-gateway"

RUN mkdir /build
ADD . /build/
WORKDIR /build

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X ${GO_MOD_PATH}/config.CommitSHA=${COMMIT_SHA} -X ${GO_MOD_PATH}/config.Version=${VERSION_TAG}" -a -o prom-aggregation-gateway .

# generate clean, final image for end users
FROM alpine
COPY --chown=nobody:nogroup --from=builder /build/prom-aggregation-gateway .

USER 65534

# executable
ENTRYPOINT [ "./prom-aggregation-gateway" ]
