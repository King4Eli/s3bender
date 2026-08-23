package com.s3bender.service;

import com.s3bender.exception.ApiException;
import com.s3bender.model.BucketEntity;
import com.s3bender.repository.BucketRepository;
import com.s3bender.web.dto.BucketSummary;
import com.s3bender.web.dto.CreateBucketResponse;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.time.Instant;
import java.util.List;
import java.util.Optional;

@Service
public class BucketService {

    private final BucketRepository repository;
    private final CryptoService cryptoService;
    private final ObjectStorageService objectStorageService;

    public BucketService(BucketRepository repository, CryptoService cryptoService,
                          ObjectStorageService objectStorageService) {
        this.repository = repository;
        this.cryptoService = cryptoService;
        this.objectStorageService = objectStorageService;
    }

    @Transactional
    public CreateBucketResponse createBucket(String name) {
        if (repository.existsById(name)) {
            throw ApiException.conflict("BucketAlreadyExists", "Bucket '" + name + "' already exists");
        }

        String accessKey = cryptoService.generateAccessKey();
        String secretKey = cryptoService.generateSecretKey();
        Instant now = Instant.now();

        BucketEntity entity = new BucketEntity(name, accessKey, cryptoService.encryptSecret(secretKey), now);
        repository.save(entity);

        try {
            objectStorageService.createBucketDirectory(name);
        } catch (IOException e) {
            repository.deleteById(name);
            throw new UncheckedIOException("Failed to provision storage for bucket '" + name + "'", e);
        }

        return new CreateBucketResponse(name, accessKey, secretKey, now);
    }

    @Transactional
    public void deleteBucket(String name) {
        BucketEntity entity = repository.findById(name)
                .orElseThrow(() -> ApiException.notFound("NoSuchBucket", "Bucket '" + name + "' does not exist"));

        if (!objectStorageService.isBucketEmpty(name)) {
            throw ApiException.conflict("BucketNotEmpty", "Bucket '" + name + "' is not empty");
        }

        repository.delete(entity);
        objectStorageService.removeBucketDirectory(name);
    }

    @Transactional(readOnly = true)
    public List<BucketSummary> listBuckets() {
        return repository.findAll().stream()
                .map(b -> new BucketSummary(b.getName(), b.getCreatedAt()))
                .toList();
    }

    @Transactional(readOnly = true)
    public BucketEntity requireBucket(String name) {
        return repository.findById(name)
                .orElseThrow(() -> ApiException.notFound("NoSuchBucket", "Bucket '" + name + "' does not exist"));
    }

    @Transactional(readOnly = true)
    public Optional<BucketEntity> findByAccessKey(String accessKey) {
        return repository.findByAccessKey(accessKey);
    }

    public String decryptedSecretFor(BucketEntity bucket) {
        return cryptoService.decryptSecret(bucket.getEncryptedSecretKey());
    }
}
