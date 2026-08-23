package com.s3bender.model;

import jakarta.persistence.Column;
import jakarta.persistence.Entity;
import jakarta.persistence.Id;
import jakarta.persistence.Table;
import jakarta.persistence.UniqueConstraint;

import java.time.Instant;

@Entity
@Table(name = "buckets", uniqueConstraints = @UniqueConstraint(columnNames = "accessKey"))
public class BucketEntity {

    /** Bucket name is the primary key; also doubles as the URL path segment. */
    @Id
    @Column(length = 63)
    private String name;

    @Column(nullable = false, length = 64)
    private String accessKey;

    /** Base64 AES-GCM ciphertext of the bucket's secret key, encrypted with the server master key. */
    @Column(nullable = false, length = 512)
    private String encryptedSecretKey;

    @Column(nullable = false)
    private Instant createdAt;

    protected BucketEntity() {
        // JPA
    }

    public BucketEntity(String name, String accessKey, String encryptedSecretKey, Instant createdAt) {
        this.name = name;
        this.accessKey = accessKey;
        this.encryptedSecretKey = encryptedSecretKey;
        this.createdAt = createdAt;
    }

    public String getName() {
        return name;
    }

    public String getAccessKey() {
        return accessKey;
    }

    public String getEncryptedSecretKey() {
        return encryptedSecretKey;
    }

    public Instant getCreatedAt() {
        return createdAt;
    }
}
