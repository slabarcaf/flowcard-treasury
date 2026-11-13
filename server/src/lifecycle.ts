/**
 * Graceful shutdown management for the FlowCard Node bridge ledger server.
 */

export interface ShutdownDependencies {
  server?: { close: (cb: (err?: Error) => void) => void; closeAllConnections?: () => void };
  pool?: { end: () => Promise<void> };
  onboardingForwarder?: { stop: () => Promise<void> };
  webhookWorker?: { stop: () => void };
  outboxWorker?: { stop: () => void };
  reconciliationWorker?: { stop: () => void };
  snapshotSchedule?: { stop: () => void };
  fundingRecoveryTimer?: NodeJS.Timeout;
  exitProcess?: (code: number) => void;
}

/**
 * Creates an idempotent, bounded graceful shutdown handler.
 * Shuts down HTTP server, intervals, background workers, forwarders, and database pool.
 * A second signal while shutdown is in progress forces immediate exit with code 1.
 */
export function createGracefulShutdown(deps: ShutdownDependencies, timeoutMs = 10_000): () => Promise<void> {
  let isShuttingDown = false;
  const exit = deps.exitProcess ?? ((code: number) => process.exit(code));

  return async () => {
    if (isShuttingDown) {
      console.warn("Forced shutdown requested (repeated termination signal)");
      if (deps.server) {
        deps.server.closeAllConnections?.();
      }
      exit(1);
      return;
    }
    isShuttingDown = true;

    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      console.error(`Graceful shutdown deadline exceeded (${Math.round(timeoutMs / 1000)}s), forcing termination`);
      if (deps.server) {
        deps.server.closeAllConnections?.();
      }
      exit(1);
    }, timeoutMs);
    timer.unref();

    try {
      // 1. Immediately cease accepting new connections so no new requests are admitted while shutting down.
      // In-flight accepted requests continue draining boundedly until server close completes.
      let serverDrainPromise: Promise<void> | undefined;
      if (deps.server) {
        serverDrainPromise = new Promise<void>((resolve, reject) => {
          deps.server!.close((err) => {
            if (err) reject(err);
            else resolve();
          });
        });
      }

      // 2. Concurrently clear intervals and stop background workers/forwarders promptly to avoid generating new work
      if (deps.fundingRecoveryTimer) {
        clearInterval(deps.fundingRecoveryTimer);
      }
      deps.snapshotSchedule?.stop();
      deps.webhookWorker?.stop();
      deps.outboxWorker?.stop();
      deps.reconciliationWorker?.stop();

      const forwarderStopPromise = deps.onboardingForwarder ? deps.onboardingForwarder.stop() : Promise.resolve();

      // Await both HTTP server drain and forwarder termination concurrently
      await Promise.all([serverDrainPromise, forwarderStopPromise]);

      if (timedOut) {
        return;
      }

      // 3. Drain database pool connections ONLY AFTER in-flight HTTP requests and workers have finished
      if (deps.pool) {
        await deps.pool.end();
      }

      clearTimeout(timer);
      exit(0);
    } catch (error) {
      if (timedOut) {
        return;
      }
      console.error("Error during graceful shutdown:", error);
      clearTimeout(timer);
      exit(1);
    }
  };
}
