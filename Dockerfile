# Release image: goreleaser (dockers_v2) builds it from the per-platform
# binaries it compiled. The integration suite builds integration-test/divisor.Dockerfile.
FROM gcr.io/distroless/static-debian13:nonroot

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/divisor /divisor

EXPOSE 8080
ENTRYPOINT ["/divisor"]
CMD ["--config", "/etc/divisor/config.yaml"]
