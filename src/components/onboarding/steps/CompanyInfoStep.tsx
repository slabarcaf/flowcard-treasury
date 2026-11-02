import { useState } from 'react';
import type { OnboardingData } from '../../../hooks/useOnboarding.js';

interface Props {
  data: OnboardingData;
  onUpdate: (patch: Partial<OnboardingData>) => void;
  onNext: () => void;
  onBack: () => void;
}

const INDUSTRIES = [
  'Software / SaaS',
  'E-commerce',
  'Professional Services',
  'Healthcare',
  'Financial Services',
  'Manufacturing',
  'Real Estate',
  'Education',
  'Media / Entertainment',
  'Other',
];

const COMPANY_TYPES = [
  'C-Corporation',
  'S-Corporation',
  'LLC',
  'Partnership',
  'Sole Proprietorship',
  'Non-profit',
];

const COUNTRIES = [
  'United States',
  'Canada',
  'United Kingdom',
  'Australia',
  'Germany',
  'France',
  'Singapore',
  'Hong Kong',
  'Japan',
  'Other',
];

export function CompanyInfoStep({ data, onUpdate, onNext, onBack }: Props) {
  const [touched, setTouched] = useState(false);

  const canProceed =
    data.legalName.trim() !== '' &&
    data.country !== '' &&
    data.phone.trim() !== '' &&
    data.industry !== '' &&
    data.companyType !== '';

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (canProceed) onNext();
  }

  const inputStyle = (isError: boolean) => ({
    backgroundColor: '#F6F5FA',
    border: `1px solid ${isError ? '#E34850' : '#E2E1E9'}`,
    color: '#25252D',
  });

  const selectStyle = (isError: boolean) => ({
    backgroundColor: '#F6F5FA',
    border: `1px solid ${isError ? '#E34850' : '#E2E1E9'}`,
    color: '#25252D',
  });

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2" style={{ color: '#25252D' }}>Company information</h2>
      <p className="text-sm mb-8" style={{ color: '#55555E' }}>Help us learn more about your business.</p>

      {/* About your company */}
      <div className="space-y-5">
        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Legal company name</label>
          <input
            type="text"
            value={data.legalName}
            onChange={(e) => onUpdate({ legalName: e.target.value })}
            placeholder="Acme Inc."
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={inputStyle(touched && !data.legalName.trim())}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && !data.legalName.trim() ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && !data.legalName.trim() ? '#E34850' : '#E2E1E9'; }}
          />
          {touched && !data.legalName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Legal name is required</p>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Country</label>
          <select
            value={data.country}
            onChange={(e) => onUpdate({ country: e.target.value })}
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={selectStyle(touched && data.country === '')}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && data.country === '' ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && data.country === '' ? '#E34850' : '#E2E1E9'; }}
          >
            <option value="">Select country</option>
            {COUNTRIES.map((c) => (
              <option key={c} value={c}>{c}</option>
            ))}
          </select>
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Phone number</label>
          <input
            type="tel"
            value={data.phone}
            onChange={(e) => onUpdate({ phone: e.target.value })}
            placeholder="+1 (555) 000-0000"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={inputStyle(touched && !data.phone.trim())}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && !data.phone.trim() ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && !data.phone.trim() ? '#E34850' : '#E2E1E9'; }}
          />
        </div>
      </div>

      {/* Divider */}
      <div className="my-8" style={{ borderTop: '1px solid #E2E1E9' }} />

      {/* Company details */}
      <p className="text-sm font-semibold mb-5" style={{ color: '#25252D' }}>Company details</p>

      <div className="space-y-5">
        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>
            Website <span style={{ color: '#8E8E99', fontWeight: 400 }}>(optional)</span>
          </label>
          <input
            type="url"
            value={data.website}
            onChange={(e) => onUpdate({ website: e.target.value })}
            placeholder="https://example.com"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={{ backgroundColor: '#F6F5FA', border: '1px solid #E2E1E9', color: '#25252D' }}
            onFocus={(e) => { e.currentTarget.style.borderColor = '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = '#E2E1E9'; }}
          />
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>
            EIN <span style={{ color: '#8E8E99', fontWeight: 400 }}>(optional)</span>
          </label>
          <input
            type="text"
            value={data.ein}
            onChange={(e) => onUpdate({ ein: e.target.value })}
            placeholder="XX-XXXXXXX"
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={{ backgroundColor: '#F6F5FA', border: '1px solid #E2E1E9', color: '#25252D' }}
            onFocus={(e) => { e.currentTarget.style.borderColor = '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = '#E2E1E9'; }}
          />
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Industry</label>
          <select
            value={data.industry}
            onChange={(e) => onUpdate({ industry: e.target.value })}
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={selectStyle(touched && data.industry === '')}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && data.industry === '' ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && data.industry === '' ? '#E34850' : '#E2E1E9'; }}
          >
            <option value="">Select industry</option>
            {INDUSTRIES.map((i) => (
              <option key={i} value={i}>{i}</option>
            ))}
          </select>
        </div>

        <div>
          <label className="block text-sm font-medium mb-1.5" style={{ color: '#55555E' }}>Company type</label>
          <select
            value={data.companyType}
            onChange={(e) => onUpdate({ companyType: e.target.value })}
            className="w-full px-4 py-3 rounded-lg text-sm outline-none transition-colors"
            style={selectStyle(touched && data.companyType === '')}
            onFocus={(e) => { e.currentTarget.style.borderColor = touched && data.companyType === '' ? '#E34850' : '#5266EB'; }}
            onBlur={(e) => { e.currentTarget.style.borderColor = touched && data.companyType === '' ? '#E34850' : '#E2E1E9'; }}
          >
            <option value="">Select type</option>
            {COMPANY_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
        </div>
      </div>

      <div className="mt-8 flex justify-center sm:justify-start">
        <button
          type="submit"
          className="w-full sm:w-auto px-8 py-3 text-sm font-semibold text-white rounded-full transition-colors cursor-pointer"
          style={{ backgroundColor: '#5266EB' }}
          onMouseEnter={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
          onMouseLeave={(e) => { e.currentTarget.style.backgroundColor = '#5266EB'; }}
          onMouseDown={(e) => { e.currentTarget.style.backgroundColor = '#3848B8'; }}
          onMouseUp={(e) => { e.currentTarget.style.backgroundColor = '#4355D0'; }}
        >
          Submit Application
        </button>
      </div>
      <div className="mt-3 text-center sm:text-left">
        <button
          type="button"
          onClick={onBack}
          className="text-sm font-medium transition-colors cursor-pointer"
          style={{ color: '#8E8E99' }}
          onMouseEnter={(e) => { e.currentTarget.style.color = '#55555E'; }}
          onMouseLeave={(e) => { e.currentTarget.style.color = '#8E8E99'; }}
        >
          Back
        </button>
      </div>
    </form>
  );
}
