package com.s3bender.web.dto;

import java.time.Instant;

public record BucketSummary(String name, Instant createdAt) {
}
