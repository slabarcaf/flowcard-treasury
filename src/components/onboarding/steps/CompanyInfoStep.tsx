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
  'Afghanistan', 'Albania', 'Algeria', 'Andorra', 'Angola', 'Antigua and Barbuda',
  'Argentina', 'Armenia', 'Australia', 'Austria', 'Azerbaijan',
  'Bahamas', 'Bahrain', 'Bangladesh', 'Barbados', 'Belarus', 'Belgium', 'Belize',
  'Benin', 'Bhutan', 'Bolivia', 'Bosnia and Herzegovina', 'Botswana', 'Brazil',
  'Brunei', 'Bulgaria', 'Burkina Faso', 'Burundi',
  'Cabo Verde', 'Cambodia', 'Cameroon', 'Canada', 'Central African Republic',
  'Chad', 'Chile', 'China', 'Colombia', 'Comoros', 'Congo', 'Costa Rica',
  'Croatia', 'Cuba', 'Cyprus', 'Czech Republic',
  'Democratic Republic of the Congo', 'Denmark', 'Djibouti', 'Dominica', 'Dominican Republic',
  'Ecuador', 'Egypt', 'El Salvador', 'Equatorial Guinea', 'Eritrea', 'Estonia',
  'Eswatini', 'Ethiopia',
  'Fiji', 'Finland', 'France',
  'Gabon', 'Gambia', 'Georgia', 'Germany', 'Ghana', 'Greece', 'Grenada',
  'Guatemala', 'Guinea', 'Guinea-Bissau', 'Guyana',
  'Haiti', 'Honduras', 'Hong Kong', 'Hungary',
  'Iceland', 'India', 'Indonesia', 'Iran', 'Iraq', 'Ireland', 'Israel', 'Italy',
  'Ivory Coast',
  'Jamaica', 'Japan', 'Jordan',
  'Kazakhstan', 'Kenya', 'Kiribati', 'Kuwait', 'Kyrgyzstan',
  'Laos', 'Latvia', 'Lebanon', 'Lesotho', 'Liberia', 'Libya', 'Liechtenstein',
  'Lithuania', 'Luxembourg',
  'Macau', 'Madagascar', 'Malawi', 'Malaysia', 'Maldives', 'Mali', 'Malta',
  'Marshall Islands', 'Mauritania', 'Mauritius', 'Mexico', 'Micronesia', 'Moldova',
  'Monaco', 'Mongolia', 'Montenegro', 'Morocco', 'Mozambique', 'Myanmar',
  'Namibia', 'Nauru', 'Nepal', 'Netherlands', 'New Zealand', 'Nicaragua', 'Niger',
  'Nigeria', 'North Korea', 'North Macedonia', 'Norway',
  'Oman',
  'Pakistan', 'Palau', 'Palestine', 'Panama', 'Papua New Guinea', 'Paraguay',
  'Peru', 'Philippines', 'Poland', 'Portugal',
  'Qatar',
  'Romania', 'Russia', 'Rwanda',
  'Saint Kitts and Nevis', 'Saint Lucia', 'Saint Vincent and the Grenadines',
  'Samoa', 'San Marino', 'Sao Tome and Principe', 'Saudi Arabia', 'Senegal',
  'Serbia', 'Seychelles', 'Sierra Leone', 'Singapore', 'Slovakia', 'Slovenia',
  'Solomon Islands', 'Somalia', 'South Africa', 'South Korea', 'South Sudan',
  'Spain', 'Sri Lanka', 'Sudan', 'Suriname', 'Sweden', 'Switzerland', 'Syria',
  'Taiwan', 'Tajikistan', 'Tanzania', 'Thailand', 'Timor-Leste', 'Togo', 'Tonga',
  'Trinidad and Tobago', 'Tunisia', 'Turkey', 'Turkmenistan', 'Tuvalu',
  'Uganda', 'Ukraine', 'United Arab Emirates', 'United Kingdom', 'United States',
  'Uruguay', 'Uzbekistan',
  'Vanuatu', 'Vatican City', 'Venezuela', 'Vietnam',
  'Yemen',
  'Zambia', 'Zimbabwe',
];

export function CompanyInfoStep({ data, onUpdate, onNext }: Props) {
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

  return (
    <form onSubmit={handleSubmit} className="animate-step-in">
      <h2 className="text-2xl font-bold mb-2 text-white">Tell us about your business</h2>
      <p className="text-sm mb-8" style={{ color: '#8B95A5' }}>Help us learn more about your company.</p>

      <div className="space-y-4">
        <div>
          <input
            type="text"
            value={data.legalName}
            onChange={(e) => onUpdate({ legalName: e.target.value })}
            placeholder="Business legal name"
            className="revolut-input w-full"
            style={{ borderColor: touched && !data.legalName.trim() ? '#E34850' : undefined }}
          />
          {touched && !data.legalName.trim() && (
            <p className="text-xs mt-1" style={{ color: '#E34850' }}>Legal name is required</p>
          )}
        </div>

        <div>
          <select
            value={data.country}
            onChange={(e) => onUpdate({ country: e.target.value })}
            className="revolut-select w-full"
            style={{ borderColor: touched && data.country === '' ? '#E34850' : undefined }}
          >
            <option value="">Country of incorporation</option>
            {COUNTRIES.map((c) => (
              <option key={c} value={c}>{c}</option>
            ))}
          </select>
        </div>

        <div>
          <input
            type="tel"
            value={data.phone}
            onChange={(e) => onUpdate({ phone: e.target.value })}
            placeholder="Phone number"
            className="revolut-input w-full"
            style={{ borderColor: touched && !data.phone.trim() ? '#E34850' : undefined }}
          />
        </div>

        <div>
          <select
            value={data.companyType}
            onChange={(e) => onUpdate({ companyType: e.target.value })}
            className="revolut-select w-full"
            style={{ borderColor: touched && data.companyType === '' ? '#E34850' : undefined }}
          >
            <option value="">Legal business type</option>
            {COMPANY_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
        </div>

        <div>
          <select
            value={data.industry}
            onChange={(e) => onUpdate({ industry: e.target.value })}
            className="revolut-select w-full"
            style={{ borderColor: touched && data.industry === '' ? '#E34850' : undefined }}
          >
            <option value="">Industry</option>
            {INDUSTRIES.map((i) => (
              <option key={i} value={i}>{i}</option>
            ))}
          </select>
        </div>

        {/* Optional fields */}
        <div>
          <input
            type="url"
            value={data.website}
            onChange={(e) => onUpdate({ website: e.target.value })}
            placeholder="Website"
            className="revolut-input w-full"
          />
          <p className="text-xs mt-1" style={{ color: '#5A6577' }}>Optional</p>
        </div>

        <div>
          <input
            type="text"
            value={data.ein}
            onChange={(e) => onUpdate({ ein: e.target.value })}
            placeholder="EIN (Registration number)"
            className="revolut-input w-full"
          />
          <p className="text-xs mt-1" style={{ color: '#5A6577' }}>Optional</p>
        </div>
      </div>

      <div className="mt-auto pt-10">
        <button type="submit" className="revolut-btn-primary w-full">
          Continue
        </button>
      </div>
    </form>
  );
}
