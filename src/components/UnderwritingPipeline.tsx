import type { PipelineStep } from '../state/types.js';

interface Props {
  precheck: PipelineStep[];
  perRequest: PipelineStep[];
}

const STATUS_STYLES: Record<PipelineStep['status'], { bg: string; border: string; text: string }> = {
  pass: { bg: 'bg-emerald-50', border: 'border-emerald-300', text: 'text-emerald-700' },
  fail: { bg: 'bg-red-50', border: 'border-red-300', text: 'text-red-700' },
  skip: { bg: 'bg-gray-50', border: 'border-gray-200', text: 'text-gray-400' },
  flag: { bg: 'bg-orange-50', border: 'border-orange-300', text: 'text-orange-700' },
};

function StepNode({ step }: { step: PipelineStep }) {
  const style = STATUS_STYLES[step.status];
  return (
    <div className="relative group">
      <div
        className={`${style.bg} ${style.border} border rounded-lg px-3 py-2 min-w-[120px] text-center ${step.tooltip ? 'cursor-help' : ''}`}
      >
        <div className={`text-xs font-semibold ${style.text}`}>{step.label}</div>
        {step.sublabel && <div className="text-[10px] text-gray-400 mt-0.5">{step.sublabel}</div>}
        {step.value && (
          <div className={`text-xs font-mono mt-1 ${style.text} font-medium`}>{step.value}</div>
        )}
      </div>
      {step.tooltip && (
        <div className="absolute top-full left-1/2 -translate-x-1/2 mt-2 w-56 px-3 py-2 bg-gray-900 text-white text-[11px] leading-relaxed rounded-lg shadow-lg opacity-0 invisible group-hover:opacity-100 group-hover:visible transition-all duration-150 z-50 pointer-events-none">
          <div className="absolute bottom-full left-1/2 -translate-x-1/2 mb-[-1px] border-4 border-transparent border-b-gray-900" />
          {step.tooltip}
        </div>
      )}
    </div>
  );
}

function Arrow({ failed }: { failed?: boolean }) {
  return (
    <div className="flex items-center px-1 shrink-0">
      <svg
        width="24"
        height="12"
        viewBox="0 0 24 12"
        fill="none"
        className={failed ? 'opacity-30' : 'opacity-60'}
      >
        <line
          x1="0"
          y1="6"
          x2="18"
          y2="6"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeDasharray={failed ? '3 3' : undefined}
          className="text-gray-400"
        />
        <polyline
          points="15,2 20,6 15,10"
          stroke="currentColor"
          strokeWidth="1.5"
          fill="none"
          className="text-gray-400"
        />
      </svg>
    </div>
  );
}

export function UnderwritingPipeline({ precheck, perRequest }: Props) {
  if (precheck.length === 0 && perRequest.length === 0) return null;

  // Determine result box labels based on which lane failed
  const precheckFailed = precheck.some((s) => s.status === 'fail');

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-5 animate-panel-in space-y-4 overflow-visible">
      <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider">
        Underwriting Pipeline
      </h4>

      <PrecheckLane steps={precheck} />

      {!precheckFailed && perRequest.length > 0 && (
        <PerRequestLane steps={perRequest} />
      )}
    </div>
  );
}

function PrecheckLane({ steps }: { steps: PipelineStep[] }) {
  const firstFailIdx = steps.findIndex((s) => s.status === 'fail');

  return (
    <div>
      <div className="text-[10px] font-semibold text-gray-400 uppercase tracking-wider mb-2">
        Daily precheck
      </div>
      <div className="flex flex-wrap items-center gap-y-2 pb-1 overflow-visible">
        {steps.map((step, i) => (
          <div key={i} className="flex items-center">
            {i > 0 && <Arrow failed={firstFailIdx >= 0 && i > firstFailIdx} />}
            <StepNode step={step} />
          </div>
        ))}
        {firstFailIdx >= 0 && (
          <>
            <Arrow failed />
            <div className="bg-red-100 border border-red-300 border-dashed rounded-lg px-3 py-2 min-w-[90px] text-center">
              <div className="text-xs font-semibold text-red-600">No line</div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function PerRequestLane({ steps }: { steps: PipelineStep[] }) {
  const firstFailIdx = steps.findIndex((s) => s.status === 'fail');

  return (
    <div>
      <div className="text-[10px] font-semibold text-gray-400 uppercase tracking-wider mb-2">
        Per request
      </div>
      <div className="flex flex-wrap items-center gap-y-2 pb-1 overflow-visible">
        {steps.map((step, i) => (
          <div key={i} className="flex items-center">
            {i > 0 && <Arrow failed={firstFailIdx >= 0 && i > firstFailIdx} />}
            <StepNode step={step} />
          </div>
        ))}
        {firstFailIdx >= 0 && (
          <>
            <Arrow failed />
            <div className="bg-red-100 border border-red-300 border-dashed rounded-lg px-3 py-2 min-w-[90px] text-center">
              <div className="text-xs font-semibold text-red-600">No bridge</div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
