package com.s3bender.web.dto;

import java.time.Instant;

public record PresignResponse(String url, String method, Instant expiresAt) {
}
