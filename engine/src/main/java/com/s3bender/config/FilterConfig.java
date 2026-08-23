package com.s3bender.config;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.s3bender.service.BucketService;
import com.s3bender.service.SignatureService;
import com.s3bender.web.filter.AdminAuthFilter;
import com.s3bender.web.filter.BucketAuthFilter;
import jakarta.servlet.Filter;
import org.springframework.boot.web.servlet.FilterRegistrationBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class FilterConfig {

    @Bean
    public FilterRegistrationBean<Filter> adminAuthFilter(S3BenderProperties properties, ObjectMapper objectMapper) {
        FilterRegistrationBean<Filter> registration = new FilterRegistrationBean<>(
                new AdminAuthFilter(properties, objectMapper));
        registration.addUrlPatterns("/admin/*");
        registration.setOrder(1);
        return registration;
    }

    @Bean
    public FilterRegistrationBean<Filter> bucketAuthFilter(BucketService bucketService,
                                                             SignatureService signatureService,
                                                             S3BenderProperties properties,
                                                             ObjectMapper objectMapper) {
        FilterRegistrationBean<Filter> registration = new FilterRegistrationBean<>(
                new BucketAuthFilter(bucketService, signatureService, properties, objectMapper));
        registration.addUrlPatterns("/buckets/*");
        registration.setOrder(2);
        return registration;
    }
}
