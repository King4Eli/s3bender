package com.s3bender.web.dto;

import java.time.Instant;

public record ObjectSummary(String key, long size, Instant lastModified, String etag) {
}
