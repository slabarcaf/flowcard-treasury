import { describe, expect, it, vi } from "vitest";
import http from "node:http";
import { AddressInfo } from "node:net";
import { createGracefulShutdown } from "../../server/src/lifecycle.js";

describe("graceful shutdown lifecycle", () => {
  it("allows in-flight HTTP request to complete before draining pool and exiting", async () => {
    let resolveRequest: () => void = () => {};
    const requestReceived = new Promise<void>((resolve) => {
      resolveRequest = resolve;
    });

    let completeResponse: () => void = () => {};
    const responseCanComplete = new Promise<void>((resolve) => {
      completeResponse = resolve;
    });

    let requestFinished = false;
    let poolEnded = false;

    const server = http.createServer((_req, res) => {
      resolveRequest();
      void responseCanComplete.then(() => {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ status: "success" }));
        requestFinished = true;
      });
    });

    await new Promise<void>((resolve) => {
      server.listen(0, "127.0.0.1", () => resolve());
    });
    const port = (server.address() as AddressInfo).port;

    const poolEnd = vi.fn(async () => {
      poolEnded = true;
      expect(requestFinished).toBe(true); // pool.end must occur strictly AFTER in-flight request finishes
    });
    const pool = { end: poolEnd };

    const exitProcess = vi.fn();
    const shutdown = createGracefulShutdown({
      server,
      pool,
      exitProcess,
    }, 5000);

    // Send HTTP request to local server
    const clientReq = http.get(`http://127.0.0.1:${port}/test`);
    let clientStatus = 0;
    const clientResponsePromise = new Promise<void>((resolve, reject) => {
      clientReq.on("response", (res) => {
        clientStatus = res.statusCode ?? 0;
        res.on("data", () => {});
        res.on("end", () => resolve());
      });
      clientReq.on("error", reject);
    });

    // Wait until server has accepted and entered request handler
    await requestReceived;

    // Trigger graceful shutdown while request is in-flight
    const shutdownPromise = shutdown();

    // Verify pool is NOT ended yet while request is in-flight
    expect(poolEnded).toBe(false);
    expect(exitProcess).not.toHaveBeenCalled();

    // Allow in-flight request to finish responding
    completeResponse();
    await clientResponsePromise;

    expect(clientStatus).toBe(200);
    expect(requestFinished).toBe(true);

    // Now shutdown should complete cleanly
    await shutdownPromise;

    expect(poolEnd).toHaveBeenCalledOnce();
    expect(exitProcess).toHaveBeenCalledWith(0);
  });

  it("initiates server.close immediately so new connections are rejected even if forwarder stop is delayed", async () => {
    const server = http.createServer((_req, res) => {
      res.writeHead(200);
      res.end("ok");
    });

    await new Promise<void>((resolve) => {
      server.listen(0, "127.0.0.1", () => resolve());
    });
    const port = (server.address() as AddressInfo).port;

    let forwarderStopComplete = false;
    let finishForwarderStop: () => void = () => {};
    const forwarderStopPromise = new Promise<void>((resolve) => {
      finishForwarderStop = () => {
        forwarderStopComplete = true;
        resolve();
      };
    });

    const onboardingForwarder = {
      stop: vi.fn(async () => forwarderStopPromise),
    };

    let poolEnded = false;
    const pool = { end: vi.fn(async () => { poolEnded = true; }) };
    const exitProcess = vi.fn();

    const shutdown = createGracefulShutdown({
      server,
      pool,
      exitProcess,
      onboardingForwarder: onboardingForwarder as any,
    }, 5000);

    // Trigger shutdown; forwarder stop will be held pending
    const shutdownPromise = shutdown();

    // Verify forwarder stop was called
    expect(onboardingForwarder.stop).toHaveBeenCalledOnce();
    expect(forwarderStopComplete).toBe(false);

    // Attempting a new connection while forwarder is still shutting down should fail immediately
    // because server.close() was initiated synchronously at shutdown start
    let newConnectionRejected = false;
    await new Promise<void>((resolve) => {
      const req = http.get(`http://127.0.0.1:${port}/new`);
      req.on("error", (_err: any) => {
        newConnectionRejected = true;
        resolve();
      });
      req.on("response", () => {
        resolve();
      });
    });

    expect(newConnectionRejected).toBe(true);
    expect(poolEnded).toBe(false);

    // Now unblock the forwarder stop
    finishForwarderStop();
    await shutdownPromise;

    expect(poolEnded).toBe(true);
    expect(exitProcess).toHaveBeenCalledWith(0);
  });

  it("forces termination with code 1 and closes connections if in-flight request hangs past deadline", async () => {
    let markReceived: () => void = () => {};
    const receivedPromise = new Promise<void>((resolve) => {
      markReceived = resolve;
    });

    const server = http.createServer((_req, _res) => {
      markReceived();
      // Never completes; simulates hung handler/connection
    });

    await new Promise<void>((resolve) => {
      server.listen(0, "127.0.0.1", () => resolve());
    });
    const port = (server.address() as AddressInfo).port;

    let poolEnded = false;
    const pool = { end: vi.fn(async () => { poolEnded = true; }) };

    let exitCode: number | undefined;
    const exitProcess = vi.fn((code: number) => {
      exitCode = code;
    });

    // Short 100ms deadline
    const shutdown = createGracefulShutdown({
      server,
      pool,
      exitProcess,
    }, 100);

    const clientReq = http.get(`http://127.0.0.1:${port}/hang`);
    let clientGotError = false;
    clientReq.on("error", () => {
      clientGotError = true;
    });

    await receivedPromise;

    // Trigger shutdown while request hangs
    void shutdown();

    // Wait for 150ms (past 100ms deadline)
    await new Promise((resolve) => setTimeout(resolve, 150));

    expect(exitProcess).toHaveBeenCalledWith(1);
    expect(exitCode).toBe(1);

    // Clean up server if still open
    server.close();
    server.closeAllConnections?.();
  });

  it("handles repeated termination signals by forcing immediate exit with code 1", async () => {
    let unblockServerClose: () => void = () => {};
    const serverClose = vi.fn((cb: (err?: Error) => void) => {
      unblockServerClose = () => cb();
    });
    const server = { close: serverClose, closeAllConnections: vi.fn() };
    const exitProcess = vi.fn();

    const shutdown = createGracefulShutdown({
      server,
      exitProcess,
    }, 5000);

    // First signal initiates graceful shutdown
    const firstShutdownPromise = shutdown();

    // Second signal received while first is still pending
    await shutdown();

    expect(exitProcess).toHaveBeenCalledWith(1);

    // Cleanup pending server close
    unblockServerClose();
    await firstShutdownPromise;
  });

  it("exits with code 1 if an error occurs during shutdown", async () => {
    const serverClose = vi.fn((cb: (err?: Error) => void) => {
      cb(new Error("socket close error"));
    });
    const server = { close: serverClose, closeAllConnections: vi.fn() };
    const exitProcess = vi.fn();

    const shutdown = createGracefulShutdown({
      server,
      exitProcess,
    }, 5000);

    await shutdown();

    expect(exitProcess).toHaveBeenCalledWith(1);
  });
});
