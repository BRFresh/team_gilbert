# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS build
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/*.go ./
RUN go test ./...
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/upgrade-checker .

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/BRFresh/team_gilbert"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/upgrade-checker /upgrade-checker
# OpenShift may replace this with a namespace-assigned UID.
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/upgrade-checker"]
