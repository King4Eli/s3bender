package com.s3bender.service;

import com.s3bender.config.S3BenderProperties;
import com.s3bender.exception.ApiException;
import com.s3bender.web.dto.ObjectSummary;
import org.springframework.stereotype.Service;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import java.util.stream.Stream;

/**
 * Stores object bytes on the local filesystem as {storage.root}/{bucket}/{key}.
 * Keys are validated to stay within their bucket directory (no traversal, no absolute paths).
 */
@Service
public class ObjectStorageService {

    private final Path root;

    public ObjectStorageService(S3BenderProperties properties) {
        this.root = Path.of(properties.getStorage().getRoot()).toAbsolutePath().normalize();
        try {
            Files.createDirectories(root);
        } catch (IOException e) {
            throw new IllegalStateException("Failed to initialize storage root at " + root, e);
        }
    }

    public void createBucketDirectory(String bucket) throws IOException {
        Files.createDirectories(bucketDir(bucket));
    }

    public void removeBucketDirectory(String bucket) {
        try {
            Files.deleteIfExists(bucketDir(bucket));
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    public boolean isBucketEmpty(String bucket) {
        Path dir = bucketDir(bucket);
        if (!Files.isDirectory(dir)) {
            return true;
        }
        try (Stream<Path> entries = Files.list(dir)) {
            return entries.findAny().isEmpty();
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    /** Streams the request body to disk and returns the resulting object's ETag (hex MD5). */
    public String putObject(String bucket, String key, InputStream body) {
        Path target = resolveObjectPath(bucket, key);
        try {
            Files.createDirectories(target.getParent());
            Path tmp = Files.createTempFile(target.getParent(), ".upload-", ".tmp");
            MessageDigest md5 = MessageDigest.getInstance("MD5");
            try (InputStream digestIn = new DigestInputStream(body, md5);
                 OutputStream out = Files.newOutputStream(tmp)) {
                digestIn.transferTo(out);
            }
            Files.move(tmp, target, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
            return HexFormat.of().formatHex(md5.digest());
        } catch (IOException e) {
            throw new java.io.UncheckedIOException("Failed to write object '" + key + "'", e);
        } catch (java.security.NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    public InputStream getObject(String bucket, String key) {
        Path path = resolveObjectPath(bucket, key);
        if (!Files.isRegularFile(path)) {
            throw ApiException.notFound("NoSuchKey", "Object '" + key + "' does not exist in bucket '" + bucket + "'");
        }
        try {
            return Files.newInputStream(path);
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    public ObjectSummary statObject(String bucket, String key) {
        Path path = resolveObjectPath(bucket, key);
        if (!Files.isRegularFile(path)) {
            throw ApiException.notFound("NoSuchKey", "Object '" + key + "' does not exist in bucket '" + bucket + "'");
        }
        try {
            long size = Files.size(path);
            Instant lastModified = Files.getLastModifiedTime(path).toInstant();
            return new ObjectSummary(key, size, lastModified, computeEtag(path));
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    public void deleteObject(String bucket, String key) {
        Path path = resolveObjectPath(bucket, key);
        try {
            Files.deleteIfExists(path);
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    public List<ObjectSummary> listObjects(String bucket, String prefix) {
        Path dir = bucketDir(bucket);
        if (!Files.isDirectory(dir)) {
            return List.of();
        }
        try (Stream<Path> paths = Files.walk(dir)) {
            return paths.filter(Files::isRegularFile)
                    .map(p -> dir.relativize(p).toString().replace('\\', '/'))
                    .filter(k -> prefix == null || prefix.isBlank() || k.startsWith(prefix))
                    .sorted()
                    .map(k -> {
                        Path p = dir.resolve(k);
                        try {
                            return new ObjectSummary(k, Files.size(p), Files.getLastModifiedTime(p).toInstant(),
                                    computeEtag(p));
                        } catch (IOException e) {
                            throw new java.io.UncheckedIOException(e);
                        }
                    })
                    .toList();
        } catch (IOException e) {
            throw new java.io.UncheckedIOException(e);
        }
    }

    private String computeEtag(Path path) {
        try {
            MessageDigest md5 = MessageDigest.getInstance("MD5");
            try (InputStream in = Files.newInputStream(path)) {
                byte[] buffer = new byte[8192];
                int read;
                while ((read = in.read(buffer)) != -1) {
                    md5.update(buffer, 0, read);
                }
            }
            return HexFormat.of().formatHex(md5.digest());
        } catch (Exception e) {
            throw new IllegalStateException(e);
        }
    }

    private Path bucketDir(String bucket) {
        return root.resolve(bucket).normalize();
    }

    private Path resolveObjectPath(String bucket, String key) {
        if (key == null || key.isBlank() || key.startsWith("/") || key.contains("..") || key.contains("\0")) {
            throw ApiException.badRequest("InvalidKey", "Object key is invalid");
        }
        Path bucketDir = bucketDir(bucket);
        Path resolved = bucketDir.resolve(key).normalize();
        if (!resolved.startsWith(bucketDir)) {
            throw ApiException.badRequest("InvalidKey", "Object key escapes bucket directory");
        }
        return resolved;
    }
}
