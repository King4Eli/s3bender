package com.s3bender.web.dto;

import jakarta.validation.constraints.NotBlank;
import jakarta.validation.constraints.Pattern;

public record CreateBucketRequest(
        @NotBlank
        @Pattern(
                regexp = "^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$",
                message = "must be 3-63 chars, lowercase alphanumeric and hyphens, not starting/ending with a hyphen")
        String name) {
}
