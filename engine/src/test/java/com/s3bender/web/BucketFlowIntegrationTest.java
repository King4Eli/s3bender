package com.s3bender.web;

import com.s3bender.web.dto.CreateBucketResponse;
import com.s3bender.web.dto.PresignRequest;
import com.s3bender.web.dto.PresignResponse;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.web.client.TestRestTemplate;
import org.springframework.boot.test.web.server.LocalServerPort;
import org.springframework.http.HttpEntity;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpMethod;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Instant;
import java.util.HexFormat;

import static org.assertj.core.api.Assertions.assertThat;

/**
 * End-to-end: create a bucket, upload an object with the bucket's own HMAC credentials,
 * fetch it back, then fetch it again through a presigned URL with no Authorization header at all.
 */
@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
class BucketFlowIntegrationTest {

    private static final String ADMIN_KEY = "test-admin-key";

    @TempDir
    static Path tempDir;

    @DynamicPropertySource
    static void props(DynamicPropertyRegistry registry) {
        registry.add("s3bender.auth.admin-api-key", () -> ADMIN_KEY);
        registry.add("s3bender.auth.master-key", () -> "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=");
        registry.add("s3bender.storage.root", () -> tempDir.resolve("objects").toString());
        registry.add("spring.datasource.url", () -> "jdbc:h2:mem:s3bender-" + System.nanoTime());
    }

    @LocalServerPort
    private int port;

    private final TestRestTemplate rest = new TestRestTemplate();

    @Test
    void createUploadDownloadAndPresign() {
        String bucketName = "flow-test-bucket";

        HttpHeaders adminHeaders = new HttpHeaders();
        adminHeaders.set("X-Admin-Api-Key", ADMIN_KEY);
        adminHeaders.setContentType(MediaType.APPLICATION_JSON);
        ResponseEntity<CreateBucketResponse> created = rest.exchange(
                url("/admin/buckets"), HttpMethod.POST,
                new HttpEntity<>("{\"name\":\"" + bucketName + "\"}", adminHeaders),
                CreateBucketResponse.class);
        assertThat(created.getStatusCode()).isEqualTo(HttpStatus.CREATED);
        CreateBucketResponse bucket = created.getBody();
        assertThat(bucket).isNotNull();

        String objectKey = "docs/hello.txt";
        String content = "hello s3bender";
        String path = "/buckets/" + bucketName + "/objects/" + objectKey;

        HttpHeaders putHeaders = new HttpHeaders();
        putHeaders.set(HttpHeaders.AUTHORIZATION, signHeader("PUT", path, bucket.secretKey(), bucket.accessKey()));
        ResponseEntity<Void> putResponse = rest.exchange(
                url(path), HttpMethod.PUT, new HttpEntity<>(content, putHeaders), Void.class);
        assertThat(putResponse.getStatusCode()).isEqualTo(HttpStatus.OK);

        HttpHeaders getHeaders = new HttpHeaders();
        getHeaders.set(HttpHeaders.AUTHORIZATION, signHeader("GET", path, bucket.secretKey(), bucket.accessKey()));
        ResponseEntity<String> getResponse = rest.exchange(
                url(path), HttpMethod.GET, new HttpEntity<>(getHeaders), String.class);
        assertThat(getResponse.getStatusCode()).isEqualTo(HttpStatus.OK);
        assertThat(getResponse.getBody()).isEqualTo(content);

        HttpHeaders presignHeaders = new HttpHeaders();
        presignHeaders.set(HttpHeaders.AUTHORIZATION,
                signHeader("POST", "/buckets/" + bucketName + "/presign", bucket.secretKey(), bucket.accessKey()));
        presignHeaders.setContentType(MediaType.APPLICATION_JSON);
        ResponseEntity<PresignResponse> presignResponse = rest.exchange(
                url("/buckets/" + bucketName + "/presign"), HttpMethod.POST,
                new HttpEntity<>(new PresignRequest(objectKey, "GET", 60), presignHeaders),
                PresignResponse.class);
        assertThat(presignResponse.getStatusCode()).isEqualTo(HttpStatus.OK);
        String presignedUrl = presignResponse.getBody().url();

        ResponseEntity<String> presignedGet = rest.getForEntity(presignedUrl, String.class);
        assertThat(presignedGet.getStatusCode()).isEqualTo(HttpStatus.OK);
        assertThat(presignedGet.getBody()).isEqualTo(content);
    }

    private String url(String path) {
        return "http://localhost:" + port + path;
    }

    private String signHeader(String method, String path, String secret, String accessKey) {
        long timestamp = Instant.now().getEpochSecond();
        String stringToSign = method + "\n" + path + "\n" + timestamp;
        String signature = hmacHex(secret, stringToSign);
        return "S3BENDER-HMAC-SHA256 AccessKey=" + accessKey + ",Timestamp=" + timestamp + ",Signature=" + signature;
    }

    private String hmacHex(String secret, String data) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(secret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            return HexFormat.of().formatHex(mac.doFinal(data.getBytes(StandardCharsets.UTF_8)));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }
}
