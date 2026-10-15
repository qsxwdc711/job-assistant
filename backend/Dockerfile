FROM registry-1.docker.io/library/golang:1.26.8-bookworm@sha256:dc9ad6c05acc7a88e5b71bde60a5fe3bd4b9f0db209011711b464107438a8107 AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
COPY go.mod go.sum ./
RUN GOPROXY=${GOPROXY} go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin && \
    CGO_ENABLED=0 go build -trimpath -o /out/collector ./cmd/collector

FROM build AS verify
ENV CGO_ENABLED=1 GOTOOLCHAIN=local
CMD ["go", "test", "-race", "-v", "./...", "-count=1", "-timeout=180s"]

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/api /app/api
COPY --from=build /out/admin /app/admin
COPY --from=build /out/collector /app/collector
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/api"]
