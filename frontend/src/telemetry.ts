import "zone.js";

import { metrics, SpanStatusCode } from "@opentelemetry/api";
import { SeverityNumber } from "@opentelemetry/api-logs";
import { ZoneContextManager } from "@opentelemetry/context-zone";
import { OTLPLogExporter } from "@opentelemetry/exporter-logs-otlp-http";
import {
  AggregationTemporalityPreference,
  OTLPMetricExporter,
} from "@opentelemetry/exporter-metrics-otlp-http";
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { registerInstrumentations } from "@opentelemetry/instrumentation";
import { DocumentLoadInstrumentation } from "@opentelemetry/instrumentation-document-load";
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch";
import { UserInteractionInstrumentation } from "@opentelemetry/instrumentation-user-interaction";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { BatchLogRecordProcessor, LoggerProvider } from "@opentelemetry/sdk-logs";
import { MeterProvider, PeriodicExportingMetricReader } from "@opentelemetry/sdk-metrics";
import {
  BatchSpanProcessor,
  ParentBasedSampler,
  TraceIdRatioBasedSampler,
  WebTracerProvider,
} from "@opentelemetry/sdk-trace-web";
import {
  ATTR_SERVICE_NAME,
  ATTR_SERVICE_NAMESPACE,
  ATTR_SERVICE_VERSION,
} from "@opentelemetry/semantic-conventions";
import { onCLS, onFCP, onINP, onLCP, onTTFB, type Metric } from "web-vitals";

interface BrowserTelemetryConfig {
  enabled: boolean;
  sampleRatio: number;
  serviceName: string;
  serviceNamespace: string;
  serviceVersion: string;
  deploymentEnvironment: string;
}

declare global {
  var __PROJECT_NAME_OTEL__: BrowserTelemetryConfig | undefined;
}

export function initTelemetry(): void {
  const config = globalThis.__PROJECT_NAME_OTEL__;
  if (!config?.enabled) return;

  const resource = resourceFromAttributes({
    [ATTR_SERVICE_NAME]: config.serviceName,
    [ATTR_SERVICE_NAMESPACE]: config.serviceNamespace,
    [ATTR_SERVICE_VERSION]: config.serviceVersion,
    "deployment.environment.name": config.deploymentEnvironment,
  });

  const tracerProvider = new WebTracerProvider({
    resource,
    sampler: new ParentBasedSampler({
      root: new TraceIdRatioBasedSampler(config.sampleRatio),
    }),
    spanProcessors: [
      new BatchSpanProcessor(new OTLPTraceExporter({ url: "/otel/v1/traces" }), {
        scheduledDelayMillis: 1000,
        exportTimeoutMillis: 5000,
        maxExportBatchSize: 128,
        maxQueueSize: 512,
      }),
    ],
  });
  tracerProvider.register({ contextManager: new ZoneContextManager() });

  registerInstrumentations({
    tracerProvider,
    instrumentations: [
      new DocumentLoadInstrumentation(),
      new FetchInstrumentation({
        ignoreUrls: [/\/otel\/v1\/(?:traces|metrics|logs)$/],
        propagateTraceHeaderCorsUrls: [globalThis.location.origin],
        clearTimingResources: true,
        applyCustomAttributesOnSpan(span, request) {
          const input = typeof request === "string"
            ? request
            : request instanceof Request ? request.url : undefined;
          if (!input) return;
          const url = new URL(input, globalThis.location.href);
          const sanitized = `${url.origin}${url.pathname}`;
          span.setAttribute("url.full", sanitized);
          span.setAttribute("http.url", sanitized);
        },
      }),
      new UserInteractionInstrumentation({ eventNames: ["click", "submit"] }),
    ],
  });

  const metricReader = new PeriodicExportingMetricReader({
    exporter: new OTLPMetricExporter({
      url: "/otel/v1/metrics",
      temporalityPreference: AggregationTemporalityPreference.DELTA,
    }),
    exportIntervalMillis: 15_000,
    exportTimeoutMillis: 5000,
  });
  const meterProvider = new MeterProvider({ resource, readers: [metricReader] });
  metrics.setGlobalMeterProvider(meterProvider);
  const meter = meterProvider.getMeter("project-name-frontend", config.serviceVersion);
  const vitalInstruments = {
    CLS: meter.createHistogram("browser.web_vital.cls", { unit: "1" }),
    FCP: meter.createHistogram("browser.web_vital.fcp", { unit: "ms" }),
    INP: meter.createHistogram("browser.web_vital.inp", { unit: "ms" }),
    LCP: meter.createHistogram("browser.web_vital.lcp", { unit: "ms" }),
    TTFB: meter.createHistogram("browser.web_vital.ttfb", { unit: "ms" }),
  };
  const recordVital = (metric: Metric) => {
    vitalInstruments[metric.name].record(metric.value, {
      "web_vital.rating": metric.rating,
      "navigation.type": metric.navigationType,
    });
  };
  onCLS(recordVital);
  onFCP(recordVital);
  onINP(recordVital);
  onLCP(recordVital);
  onTTFB(recordVital);

  const loggerProvider = new LoggerProvider({
    resource,
    processors: [
      new BatchLogRecordProcessor({
        exporter: new OTLPLogExporter({ url: "/otel/v1/logs" }),
        scheduledDelayMillis: 1000,
        exportTimeoutMillis: 5000,
        maxExportBatchSize: 64,
        maxQueueSize: 256,
      }),
    ],
    logRecordLimits: { attributeCountLimit: 16, attributeValueLengthLimit: 512 },
  });
  const logger = loggerProvider.getLogger("project-name-frontend", config.serviceVersion);
  const tracer = tracerProvider.getTracer("project-name-frontend", config.serviceVersion);

  const reportError = (type: string, message: unknown, error?: unknown) => {
    const safeMessage = String(message || "JavaScript error").slice(0, 512);
    const span = tracer.startSpan("browser.javascript_error", {
      attributes: { "error.type": type, "error.message": safeMessage },
    });
    if (error instanceof Error) span.recordException(error);
    span.setStatus({ code: SpanStatusCode.ERROR, message: type });
    span.end();
    logger.emit({
      severityNumber: SeverityNumber.ERROR,
      severityText: "ERROR",
      body: "Browser JavaScript error",
      attributes: { "error.type": type, "error.message": safeMessage },
    });
  };

  globalThis.addEventListener("error", (event) => {
    reportError(event.error?.name ?? "Error", event.message, event.error);
  });
  globalThis.addEventListener("unhandledrejection", (event) => {
    const reason = event.reason as { name?: string; message?: string } | undefined;
    reportError(reason?.name ?? "UnhandledRejection", reason?.message ?? reason, reason);
  });
  globalThis.addEventListener("pagehide", () => {
    void tracerProvider.forceFlush();
    void meterProvider.forceFlush();
    void loggerProvider.forceFlush();
  });
}
