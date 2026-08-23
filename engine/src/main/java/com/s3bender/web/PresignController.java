package com.s3bender.web;

import com.s3bender.config.S3BenderProperties;
import com.s3bender.exception.ApiException;
import com.s3bender.model.BucketEntity;
import com.s3bender.service.PresignService;
import com.s3bender.web.dto.PresignRequest;
import com.s3bender.web.dto.PresignResponse;
import com.s3bender.web.filter.BucketAuthFilter;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.Valid;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/buckets/{bucket}")
public class PresignController {

    private final PresignService presignService;
    private final S3BenderProperties properties;

    public PresignController(PresignService presignService, S3BenderProperties properties) {
        this.presignService = presignService;
        this.properties = properties;
    }

    @PostMapping("/presign")
    public PresignResponse presign(@PathVariable String bucket, @Valid @RequestBody PresignRequest request,
                                    HttpServletRequest servletRequest) {
        BucketEntity authenticated = requireAuthenticated(servletRequest, bucket);
        String baseUrl = properties.getPublicBaseUrl() != null && !properties.getPublicBaseUrl().isBlank()
                ? properties.getPublicBaseUrl()
                : servletRequest.getScheme() + "://" + servletRequest.getServerName() + ":" + servletRequest.getServerPort();
        return presignService.presign(authenticated, request, baseUrl);
    }

    private BucketEntity requireAuthenticated(HttpServletRequest request, String bucket) {
        Object attr = request.getAttribute(BucketAuthFilter.BUCKET_ATTRIBUTE);
        if (!(attr instanceof BucketEntity authenticated) || !authenticated.getName().equals(bucket)) {
            throw ApiException.unauthorized("Unauthorized", "Request was not authenticated for bucket '" + bucket + "'");
        }
        return authenticated;
    }
}
