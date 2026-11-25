# Built with src/ as the context (see compose.yaml).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/sites-admin . && mkdir -p /out/sites

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sites-admin /sites-admin
# A new named volume copies this directory, so the data volume starts owned by the nonroot user (65532).
COPY --from=build --chown=65532:65532 /out/sites /srv/sites
ENV SITES_DIR=/srv/sites
EXPOSE 8080
ENTRYPOINT ["/sites-admin"]
