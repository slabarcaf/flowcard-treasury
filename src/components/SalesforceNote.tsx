import { BLOOM_CRM_NOTE } from '../seed.js';

interface Props {
  visible: boolean;
}

export function SalesforceNote({ visible }: Props) {
  if (!visible) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg p-4 animate-panel-in">
      <div className="flex items-center gap-2 mb-2">
        <div className="w-5 h-5 rounded bg-[#00A1E0] flex items-center justify-center">
          <span className="text-[10px] text-white font-bold">SF</span>
        </div>
        <h4 className="text-xs font-semibold text-gray-500 uppercase tracking-wider">
          Salesforce Case Note
        </h4>
      </div>
      <div className="text-xs text-gray-400 mb-2">
        {BLOOM_CRM_NOTE.author} · {BLOOM_CRM_NOTE.timestamp}
      </div>
      <blockquote className="text-sm text-gray-700 italic border-l-2 border-[#00A1E0] pl-3 leading-relaxed">
        &ldquo;{BLOOM_CRM_NOTE.text}&rdquo;
      </blockquote>
    </div>
  );
}
