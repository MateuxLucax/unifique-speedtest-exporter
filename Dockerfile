# Build a static binary, then ship it on distroless — no Node, no Chromium.
FROM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/exporter .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/exporter /exporter

ENV PORT=3000
EXPOSE 3000

ENTRYPOINT ["/exporter"]
