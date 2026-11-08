FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH}
RUN mkdir -p /out/data \
    && chown 65532:65532 /out/data \
    && go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/hub ./cmd/hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hub /usr/local/bin/hub
COPY --from=build --chown=65532:65532 /out/data /data
ENV EASYBOTCE_LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/hub"]
