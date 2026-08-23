package com.s3bender.web;

import com.s3bender.service.BucketService;
import com.s3bender.web.dto.BucketSummary;
import com.s3bender.web.dto.CreateBucketRequest;
import com.s3bender.web.dto.CreateBucketResponse;
import jakarta.validation.Valid;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

import java.util.List;

/**
 * Control-plane API for provisioning buckets. Every route here requires the shared
 * X-Admin-Api-Key header (enforced by AdminAuthFilter) - it is the only credential that spans
 * buckets. Per-bucket access/secret keys returned by createBucket are shown exactly once.
 */
@RestController
@RequestMapping("/admin/buckets")
public class AdminController {

    private final BucketService bucketService;

    public AdminController(BucketService bucketService) {
        this.bucketService = bucketService;
    }

    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public CreateBucketResponse createBucket(@Valid @RequestBody CreateBucketRequest request) {
        return bucketService.createBucket(request.name());
    }

    @GetMapping
    public List<BucketSummary> listBuckets() {
        return bucketService.listBuckets();
    }

    @DeleteMapping("/{name}")
    @ResponseStatus(HttpStatus.NO_CONTENT)
    public void deleteBucket(@PathVariable String name) {
        bucketService.deleteBucket(name);
    }
}
