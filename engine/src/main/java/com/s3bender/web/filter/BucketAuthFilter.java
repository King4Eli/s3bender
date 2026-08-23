package com.s3bender.web.filter;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.s3bender.config.S3BenderProperties;
import com.s3bender.model.BucketEntity;
import com.s3bender.service.BucketService;
import com.s3bender.service.SignatureService;
import com.s3bender.web.dto.ErrorResponse;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.http.MediaType;
import org.springframework.web.filter.OncePerRequestFilter;
import org.springframework.web.util.UriUtils;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Optional;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Authenticates every /buckets/{name}/** request against that bucket's own access/secret key pair.
 *
 * Two accepted credential forms (see /_docs/auth-and-signing.md):
 *   1. Authorization header: S3BENDER-HMAC-SHA256 AccessKey=..,Timestamp=..,Signature=..
 *   2. Presigned query string (GET/PUT only): ?AccessKey=..&Expires=..&Signature=..
 */
public class BucketAuthFilter extends OncePerRequestFilter {

    public static final String BUCKET_ATTRIBUTE = "s3bender.bucket";

    private static final Pattern BUCKET_PATH = Pattern.compile("^/buckets/([^/]+)(/.*)?$");
    private static final Pattern AUTH_HEADER = Pattern.compile(
            "^S3BENDER-HMAC-SHA256\\s+AccessKey=([^,]+),\\s*Timestamp=([^,]+),\\s*Signature=([0-9a-fA-F]+)$");

    private final BucketService bucketService;
    private final SignatureService signatureService;
    private final S3BenderProperties properties;
    private final ObjectMapper objectMapper;

    public BucketAuthFilter(BucketService bucketService, SignatureService signatureService,
                             S3BenderProperties properties, ObjectMapper objectMapper) {
        this.bucketService = bucketService;
        this.signatureService = signatureService;
        this.properties = properties;
        this.objectMapper = objectMapper;
    }

    @Override
    protected void doFilterInternal(HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {

        String decodedPath = UriUtils.decode(request.getRequestURI(), StandardCharsets.UTF_8);
        Matcher pathMatcher = BUCKET_PATH.matcher(decodedPath);
        if (!pathMatcher.matches()) {
            chain.doFilter(request, response);
            return;
        }
        String bucketName = pathMatcher.group(1);

        Optional<BucketEntity> bucketOpt = bucketService.findByAccessKey(
                request.getParameter("AccessKey") != null
                        ? request.getParameter("AccessKey")
                        : extractHeaderAccessKey(request));

        if (bucketOpt.isEmpty() || !bucketOpt.get().getName().equals(bucketName)) {
            reject(response, HttpServletResponse.SC_UNAUTHORIZED, "InvalidAccessKey",
                    "No such bucket, or credentials do not belong to bucket '" + bucketName + "'");
            return;
        }
        BucketEntity bucket = bucketOpt.get();
        String secret = bucketService.decryptedSecretFor(bucket);
        String method = request.getMethod();

        boolean hasPresignParams = request.getParameter("AccessKey") != null
                && request.getParameter("Expires") != null
                && request.getParameter("Signature") != null;

        boolean authenticated;
        if (hasPresignParams) {
            authenticated = ("GET".equals(method) || "PUT".equals(method) || "HEAD".equals(method))
                    && verifyPresigned(request, decodedPath, secret);
        } else {
            authenticated = verifyHeader(request, decodedPath, secret);
        }

        if (!authenticated) {
            reject(response, HttpServletResponse.SC_FORBIDDEN, "SignatureMismatch",
                    "Request signature is invalid, expired, or malformed");
            return;
        }

        request.setAttribute(BUCKET_ATTRIBUTE, bucket);
        chain.doFilter(request, response);
    }

    private boolean verifyPresigned(HttpServletRequest request, String path, String secret) {
        long expires;
        try {
            expires = Long.parseLong(request.getParameter("Expires"));
        } catch (NumberFormatException e) {
            return false;
        }
        if (Instant.now().getEpochSecond() > expires) {
            return false;
        }
        String stringToSign = signatureService.stringToSignForPresign(request.getMethod(), path, expires);
        String expected = signatureService.sign(secret, stringToSign);
        return signatureService.matches(expected, request.getParameter("Signature"));
    }

    private boolean verifyHeader(HttpServletRequest request, String path, String secret) {
        String header = request.getHeader("Authorization");
        if (header == null) {
            return false;
        }
        Matcher m = AUTH_HEADER.matcher(header.trim());
        if (!m.matches()) {
            return false;
        }
        long timestamp;
        try {
            timestamp = Long.parseLong(m.group(2));
        } catch (NumberFormatException e) {
            return false;
        }
        long skew = Math.abs(Instant.now().getEpochSecond() - timestamp);
        if (skew > properties.getSigning().getClockSkewSeconds()) {
            return false;
        }
        String stringToSign = signatureService.stringToSignForHeader(request.getMethod(), path, timestamp);
        String expected = signatureService.sign(secret, stringToSign);
        return signatureService.matches(expected, m.group(3));
    }

    private String extractHeaderAccessKey(HttpServletRequest request) {
        String header = request.getHeader("Authorization");
        if (header == null) {
            return null;
        }
        Matcher m = AUTH_HEADER.matcher(header.trim());
        return m.matches() ? m.group(1) : null;
    }

    private void reject(HttpServletResponse response, int status, String code, String message) throws IOException {
        response.setStatus(status);
        response.setContentType(MediaType.APPLICATION_JSON_VALUE);
        response.getWriter().write(objectMapper.writeValueAsString(ErrorResponse.of(code, message)));
    }
}
