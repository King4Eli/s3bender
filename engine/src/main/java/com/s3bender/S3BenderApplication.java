package com.s3bender;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.context.properties.ConfigurationPropertiesScan;

@SpringBootApplication
@ConfigurationPropertiesScan
public class S3BenderApplication {

    public static void main(String[] args) {
        SpringApplication.run(S3BenderApplication.class, args);
    }
}
