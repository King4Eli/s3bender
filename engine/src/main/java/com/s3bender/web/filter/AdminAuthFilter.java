package com.s3bender.web.filter;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.s3bender.config.S3BenderProperties;
import com.s3bender.web.dto.ErrorResponse;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.http.MediaType;
import org.springframework.web.filter.OncePerRequestFilter;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;

/** Guards every /admin/** route with a single shared secret (X-Admin-Api-Key). */
public class AdminAuthFilter extends OncePerRequestFilter {

    public static final String HEADER = "X-Admin-Api-Key";

    private final S3BenderProperties properties;
    private final ObjectMapper objectMapper;

    public AdminAuthFilter(S3BenderProperties properties, ObjectMapper objectMapper) {
        this.properties = properties;
        this.objectMapper = objectMapper;
    }

    @Override
    protected void doFilterInternal(HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String configured = properties.getAuth().getAdminApiKey();
        String provided = request.getHeader(HEADER);

        if (configured == null || configured.isBlank()) {
            reject(response, HttpServletResponse.SC_INTERNAL_SERVER_ERROR, "AdminKeyNotConfigured",
                    "Server admin API key is not configured");
            return;
        }
        if (provided == null || !MessageDigest.isEqual(
                configured.getBytes(StandardCharsets.UTF_8), provided.getBytes(StandardCharsets.UTF_8))) {
            reject(response, HttpServletResponse.SC_UNAUTHORIZED, "Unauthorized",
                    "Missing or invalid " + HEADER + " header");
            return;
        }
        chain.doFilter(request, response);
    }

    private void reject(HttpServletResponse response, int status, String code, String message) throws IOException {
        response.setStatus(status);
        response.setContentType(MediaType.APPLICATION_JSON_VALUE);
        response.getWriter().write(objectMapper.writeValueAsString(ErrorResponse.of(code, message)));
    }
}
