# syntax=docker/dockerfile:1

FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /build
COPY Api.csproj .
RUN dotnet restore Api.csproj
COPY . .
RUN dotnet publish Api.csproj -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/aspnet:8.0
RUN apt-get update && apt-get install -y --no-install-recommends wget \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -r s3bender && useradd -r -g s3bender s3bender
WORKDIR /app
COPY --from=build /out .

ENV S3BENDER_STORAGE_ROOT=/data/objects \
    S3BENDER_DB_PATH=/data/db/s3bender.db \
    ASPNETCORE_HTTP_PORTS= \
    DOTNET_EnableWriteXorExecute=0

RUN mkdir -p /data/objects /data/db && chown -R s3bender:s3bender /data /app
USER s3bender

EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["dotnet", "Api.dll"]
