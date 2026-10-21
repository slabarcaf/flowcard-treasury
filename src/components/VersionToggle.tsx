export type DemoVersion = 'kit5' | 'gd';

interface Props {
  value: DemoVersion;
  onChange: (v: DemoVersion) => void;
}

const OPTIONS: { value: DemoVersion; label: string }[] = [
  { value: 'kit5', label: 'Kit 5' },
  { value: 'gd', label: 'GD original' },
];

export function VersionToggle({ value, onChange }: Props) {
  return (
    <div className="flex items-center rounded-lg border border-gray-200 bg-gray-100 p-0.5 text-xs font-medium" role="group" aria-label="Demo version">
      {OPTIONS.map((o) => (
        <button
          key={o.value}
          onClick={() => onChange(o.value)}
          aria-pressed={value === o.value}
          className={`px-3 py-1.5 rounded-md transition-colors cursor-pointer ${
            value === o.value ? 'bg-white text-gray-900 shadow-sm' : 'text-gray-500 hover:text-gray-700'
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
