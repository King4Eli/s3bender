package com.s3bender.web;

import com.s3bender.exception.ApiException;
import com.s3bender.model.BucketEntity;
import com.s3bender.service.ObjectStorageService;
import com.s3bender.web.dto.ObjectSummary;
import com.s3bender.web.filter.BucketAuthFilter;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.util.UriUtils;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;

/**
 * Object read/write/list endpoints, scoped to a bucket. Authentication is handled upstream by
 * {@link BucketAuthFilter}, which attaches the resolved {@link BucketEntity} as a request attribute.
 */
@RestController
@RequestMapping("/buckets/{bucket}")
public class ObjectController {

    private final ObjectStorageService storageService;

    public ObjectController(ObjectStorageService storageService) {
        this.storageService = storageService;
    }

    @PutMapping("/objects/**")
    public ResponseEntity<Void> putObject(@PathVariable String bucket, HttpServletRequest request)
            throws IOException {
        requireAuthenticated(request, bucket);
        String key = extractKey(request, bucket);
        String etag = storageService.putObject(bucket, key, request.getInputStream());
        return ResponseEntity.ok().eTag(etag).build();
    }

    @GetMapping("/objects/**")
    public void getObject(@PathVariable String bucket, HttpServletRequest request, HttpServletResponse response)
            throws IOException {
        requireAuthenticated(request, bucket);
        String key = extractKey(request, bucket);
        ObjectSummary summary = storageService.statObject(bucket, key);
        response.setContentType(MediaType.APPLICATION_OCTET_STREAM_VALUE);
        response.setContentLengthLong(summary.size());
        response.setHeader(HttpHeaders.ETAG, "\"" + summary.etag() + "\"");
        try (InputStream in = storageService.getObject(bucket, key)) {
            in.transferTo(response.getOutputStream());
        }
    }

    @RequestMapping(path = "/objects/**", method = RequestMethod.HEAD)
    public ResponseEntity<Void> headObject(@PathVariable String bucket, HttpServletRequest request) {
        requireAuthenticated(request, bucket);
        String key = extractKey(request, bucket);
        ObjectSummary summary = storageService.statObject(bucket, key);
        return ResponseEntity.ok()
                .eTag(summary.etag())
                .contentLength(summary.size())
                .build();
    }

    @DeleteMapping("/objects/**")
    public ResponseEntity<Void> deleteObject(@PathVariable String bucket, HttpServletRequest request) {
        requireAuthenticated(request, bucket);
        String key = extractKey(request, bucket);
        storageService.deleteObject(bucket, key);
        return ResponseEntity.noContent().build();
    }

    @GetMapping("/objects")
    public List<ObjectSummary> listObjects(@PathVariable String bucket,
                                            @RequestParam(required = false) String prefix,
                                            HttpServletRequest request) {
        requireAuthenticated(request, bucket);
        return storageService.listObjects(bucket, prefix);
    }

    private void requireAuthenticated(HttpServletRequest request, String bucket) {
        Object attr = request.getAttribute(BucketAuthFilter.BUCKET_ATTRIBUTE);
        if (!(attr instanceof BucketEntity authenticated) || !authenticated.getName().equals(bucket)) {
            throw ApiException.unauthorized("Unauthorized", "Request was not authenticated for bucket '" + bucket + "'");
        }
    }

    private String extractKey(HttpServletRequest request, String bucket) {
        String decoded = UriUtils.decode(request.getRequestURI(), StandardCharsets.UTF_8);
        String prefix = "/buckets/" + bucket + "/objects/";
        if (!decoded.startsWith(prefix) || decoded.length() == prefix.length()) {
            throw ApiException.badRequest("InvalidKey", "Missing object key");
        }
        return decoded.substring(prefix.length());
    }
}
