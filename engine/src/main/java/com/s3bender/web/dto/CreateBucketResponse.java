package com.s3bender.web.dto;

import java.time.Instant;

public record CreateBucketResponse(String name, String accessKey, String secretKey, Instant createdAt) {
}
