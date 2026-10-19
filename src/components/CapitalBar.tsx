import type { Band } from '../policy/types.js';
import { BAND_LABELS } from '../policy/index.js';

interface Props {
  lentOut: number;
  deployable: number;
  floor: number;
  band: Band;
}

const BAND_COLORS: Record<Band, string> = {
  a_lot: 'bg-emerald-500',
  normal: 'bg-sky-500',
  less: 'bg-amber-500',
  critical: 'bg-red-500',
  at_floor: 'bg-red-700',
};

const BAND_TEXT_COLORS: Record<Band, string> = {
  a_lot: 'text-emerald-600',
  normal: 'text-sky-600',
  less: 'text-amber-600',
  critical: 'text-red-600',
  at_floor: 'text-red-700',
};

const BAND_BADGE_COLORS: Record<Band, string> = {
  a_lot: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  normal: 'bg-sky-50 text-sky-700 border-sky-200',
  less: 'bg-amber-50 text-amber-700 border-amber-200',
  critical: 'bg-red-50 text-red-700 border-red-200',
  at_floor: 'bg-red-100 text-red-800 border-red-300',
};

function fmt(n: number): string {
  return '$' + n.toLocaleString('en-US');
}

export function CapitalBar({ lentOut, deployable, floor, band }: Props) {
  const total = lentOut + deployable + floor;

  const lentPct = (lentOut / total) * 100;
  const deployPct = (deployable / total) * 100;
  const floorPct = (floor / total) * 100;

  return (
    <div className="bg-white rounded-xl border border-gray-200 p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-3">
          <h2 className="text-sm font-semibold text-gray-900">Bridge Facility</h2>
          <span
            className={`text-xs font-medium px-2.5 py-1 rounded-full border ${BAND_BADGE_COLORS[band]}`}
          >
            {BAND_LABELS[band]}
          </span>
        </div>
        <div className="flex gap-6 text-sm">
          <span className="text-gray-500">
            Lent out{' '}
            <span className="text-gray-900 font-mono font-semibold">
              {fmt(lentOut)}
            </span>
          </span>
          <span className="text-gray-500">
            Deployable{' '}
            <span className={`font-mono font-semibold ${BAND_TEXT_COLORS[band]}`}>
              {fmt(deployable)}
            </span>
          </span>
          <span className="text-gray-500">
            Floor{' '}
            <span className="text-gray-900 font-mono font-semibold">
              {fmt(floor)}
            </span>
          </span>
        </div>
      </div>

      {/* Bar */}
      <div className="h-5 rounded-md overflow-hidden flex bg-gray-100">
        <div
          className="bg-gray-400 transition-all duration-700 ease-in-out flex items-center justify-center"
          style={{ width: `${lentPct}%` }}
        >
          {lentPct > 15 && (
            <span className="text-[11px] font-mono text-white">
              {fmt(lentOut)}
            </span>
          )}
        </div>
        <div
          className={`${BAND_COLORS[band]} transition-all duration-700 ease-in-out flex items-center justify-center`}
          style={{ width: `${deployPct}%` }}
        >
          {deployPct > 10 && (
            <span className="text-[11px] font-mono text-white font-medium">
              {fmt(deployable)}
            </span>
          )}
        </div>
        <div
          className="transition-all duration-700 ease-in-out flex items-center justify-center"
          style={{
            width: `${floorPct}%`,
            background:
              'repeating-linear-gradient(45deg, #d1d5db, #d1d5db 4px, #e5e7eb 4px, #e5e7eb 8px)',
          }}
        >
          {floorPct > 8 && (
            <span className="text-[11px] font-mono text-gray-600">
              {fmt(floor)}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
