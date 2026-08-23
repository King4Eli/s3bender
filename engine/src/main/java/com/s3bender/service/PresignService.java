package com.s3bender.service;

import com.s3bender.config.S3BenderProperties;
import com.s3bender.exception.ApiException;
import com.s3bender.model.BucketEntity;
import com.s3bender.web.dto.PresignRequest;
import com.s3bender.web.dto.PresignResponse;
import org.springframework.stereotype.Service;
import org.springframework.web.util.UriComponentsBuilder;

import java.time.Instant;

@Service
public class PresignService {

    private final SignatureService signatureService;
    private final BucketService bucketService;
    private final S3BenderProperties properties;

    public PresignService(SignatureService signatureService, BucketService bucketService,
                           S3BenderProperties properties) {
        this.signatureService = signatureService;
        this.bucketService = bucketService;
        this.properties = properties;
    }

    public PresignResponse presign(BucketEntity bucket, PresignRequest request, String externalBaseUrl) {
        if (request.expiresInSeconds() > properties.getSigning().getMaxPresignExpirySeconds()) {
            throw ApiException.badRequest("InvalidExpiry",
                    "expiresInSeconds may not exceed " + properties.getSigning().getMaxPresignExpirySeconds());
        }

        String path = "/buckets/" + bucket.getName() + "/objects/" + request.key();
        long expiresAt = Instant.now().getEpochSecond() + request.expiresInSeconds();
        String secret = bucketService.decryptedSecretFor(bucket);
        String stringToSign = signatureService.stringToSignForPresign(request.method(), path, expiresAt);
        String signature = signatureService.sign(secret, stringToSign);

        String url = UriComponentsBuilder.fromHttpUrl(externalBaseUrl)
                .path(path)
                .queryParam("AccessKey", bucket.getAccessKey())
                .queryParam("Expires", expiresAt)
                .queryParam("Signature", signature)
                .build()
                .encode()
                .toUriString();

        return new PresignResponse(url, request.method(), Instant.ofEpochSecond(expiresAt));
    }
}
