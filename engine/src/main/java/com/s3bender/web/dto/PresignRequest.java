package com.s3bender.web.dto;

import jakarta.validation.constraints.Max;
import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Pattern;

public record PresignRequest(
        @NotBlank String key,
        @Pattern(regexp = "GET|PUT", message = "must be GET or PUT") String method,
        @Min(1) @Max(604800) long expiresInSeconds) {
}
