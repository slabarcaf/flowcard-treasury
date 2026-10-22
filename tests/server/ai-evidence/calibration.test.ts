import { describe, expect, it } from "vitest";
import { calibrateScore, fitCalibrationMapping } from "../../../server/src/ai-evidence/calibration.js";

describe("per-type calibration", () => {
  it("fits a versioned score mapping independently for each signal type", () => {
    const examples = [
      { signalType: "client_payment_dispute" as const, rawScore: 0.82, correct: true, labelId: "warm-1", labeledAt: "2026-10-01T00:00:00Z" },
      { signalType: "client_payment_dispute" as const, rawScore: 0.84, correct: false, labelId: "warm-2", labeledAt: "2026-10-02T00:00:00Z" },
      { signalType: "positive_note" as const, rawScore: 0.85, correct: true, labelId: "warm-3", labeledAt: "2026-10-03T00:00:00Z" },
    ];
    const disputes = fitCalibrationMapping("client_payment_dispute", "cfg-2-cal-1", examples, "2026-10-05T00:00:00Z");
    const positives = fitCalibrationMapping("positive_note", "cfg-2-cal-1", examples, "2026-10-05T00:00:00Z");

    expect(disputes.mappingVersion).toBe("empirical-score-bins-v1");
    expect(disputes.trainingLabelIds).toEqual(["warm-1", "warm-2"]);
    expect(calibrateScore(disputes, 0.83)).toBe(0.5);
    expect(calibrateScore(positives, 0.83)).toBe(1);
    expect(calibrateScore(disputes, 0.1)).toBe(0.1);
  });

  it("rejects invalid scores and duplicate warm-up labels", () => {
    const duplicate = [
      { signalType: "positive_note" as const, rawScore: 0.5, correct: true, labelId: "same", labeledAt: "2026-10-01" },
      { signalType: "positive_note" as const, rawScore: 0.6, correct: true, labelId: "same", labeledAt: "2026-10-02" },
    ];
    expect(() => fitCalibrationMapping("positive_note", "cal-v1", duplicate)).toThrow(/unique/);
    expect(() => fitCalibrationMapping("positive_note", "cal-v1", [
      { ...duplicate[0]!, rawScore: 1.1 },
    ])).toThrow(/between 0 and 1/);
  });
});
