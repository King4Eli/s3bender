# syntax=docker/dockerfile:1

FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /build
COPY pom.xml .
RUN mvn -B -q dependency:go-offline
COPY src ./src
RUN mvn -B -q package -DskipTests

FROM eclipse-temurin:21-jre-alpine
RUN addgroup -S s3bender && adduser -S s3bender -G s3bender
WORKDIR /app
COPY --from=build /build/target/s3bender.jar app.jar

ENV S3BENDER_STORAGE_ROOT=/data/objects \
    S3BENDER_DB_PATH=/data/db/s3bender

RUN mkdir -p /data/objects /data/db && chown -R s3bender:s3bender /data /app
USER s3bender

EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["java", "-jar", "/app/app.jar"]
