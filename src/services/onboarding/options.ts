/** Stable ISO values for wire payloads; labels stay in ordinary customer language. */
export type FormOption = { value: string; label: string };
const regionNames = new Intl.DisplayNames(['en'], { type: 'region' });
const countryCodes = `AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW`.split(' ');
export const COUNTRY_OPTIONS: FormOption[] = countryCodes.map(value => ({ value, label: regionNames.of(value) ?? value })).sort((a, b) => a.label.localeCompare(b.label, 'en'));
// The current readiness validator supports US-registered businesses only.
export const REGISTRATION_COUNTRIES = [{ value: 'US', label: 'United States' }];
export const US_STATES: FormOption[] = [
  ['AL', 'Alabama'], ['AK', 'Alaska'], ['AZ', 'Arizona'], ['AR', 'Arkansas'],
  ['CA', 'California'], ['CO', 'Colorado'], ['CT', 'Connecticut'], ['DE', 'Delaware'],
  ['DC', 'District of Columbia'], ['FL', 'Florida'], ['GA', 'Georgia'], ['HI', 'Hawaii'],
  ['ID', 'Idaho'], ['IL', 'Illinois'], ['IN', 'Indiana'], ['IA', 'Iowa'],
  ['KS', 'Kansas'], ['KY', 'Kentucky'], ['LA', 'Louisiana'], ['ME', 'Maine'],
  ['MD', 'Maryland'], ['MA', 'Massachusetts'], ['MI', 'Michigan'], ['MN', 'Minnesota'],
  ['MS', 'Mississippi'], ['MO', 'Missouri'], ['MT', 'Montana'], ['NE', 'Nebraska'],
  ['NV', 'Nevada'], ['NH', 'New Hampshire'], ['NJ', 'New Jersey'], ['NM', 'New Mexico'],
  ['NY', 'New York'], ['NC', 'North Carolina'], ['ND', 'North Dakota'], ['OH', 'Ohio'],
  ['OK', 'Oklahoma'], ['OR', 'Oregon'], ['PA', 'Pennsylvania'], ['RI', 'Rhode Island'],
  ['SC', 'South Carolina'], ['SD', 'South Dakota'], ['TN', 'Tennessee'], ['TX', 'Texas'],
  ['UT', 'Utah'], ['VT', 'Vermont'], ['VA', 'Virginia'], ['WA', 'Washington'],
  ['WV', 'West Virginia'], ['WI', 'Wisconsin'], ['WY', 'Wyoming'],
].map(([value, label]) => ({ value, label }));
// Supported values in the current Airwallex adapter, not invented request enums.
export const IDENTIFICATION_TYPES: FormOption[] = [
  { value: 'PASSPORT', label: 'Passport' },
  { value: 'DRIVERS_LICENSE', label: "Driver’s license" },
  { value: 'PERSONAL_ID', label: 'National identity card' },
  { value: 'SSN', label: 'Social Security number (SSN)' },
  { value: 'ITIN', label: 'Individual taxpayer identification number (ITIN)' },
];

// Intended usage values documented in the Airwallex Accounts API.
export const PRODUCT_OPTIONS: FormOption[] = [
  { value: 'CREATE_CARDS', label: 'Business cards' },
  { value: 'MANAGE_EXPENSES', label: 'Expense management' },
  { value: 'MAKE_TRANSFERS', label: 'Send transfers' },
  { value: 'RECEIVE_TRANSFERS', label: 'Receive transfers' },
  { value: 'CONVERT_FUNDS', label: 'Currency conversion' },
  { value: 'ACCEPT_ONLINE_PAYMENTS', label: 'Accept online payments' },
  { value: 'COLLECT_MARKETPLACE_PROCEEDS', label: 'Collect marketplace proceeds' },
  { value: 'GET_PAID', label: 'Get paid' },
  { value: 'USE_AWX_API', label: 'API integration' },
  { value: 'TRANSFER_CNY_INBOUND', label: 'Receive CNY transfers' },
];
export const PLATFORM_ROLE_OPTIONS: FormOption[] = [
  { value: 'BENEFICIAL_OWNER', label: 'Beneficial owner' },
  { value: 'CONTROLLING_PERSON', label: 'Controlling person' },
  { value: 'AUTHORISED_PERSON', label: 'Authorized representative' },
];
const currencyNames = new Intl.DisplayNames(['en'], { type: 'currency' });
export const CURRENCY_OPTIONS: FormOption[] = Intl.supportedValuesOf('currency')
  .map(value => ({ value, label: `${value} — ${currencyNames.of(value) ?? value}` }));
export const DOCUMENT_TYPES: FormOption[] = [
  { value: 'BUSINESS_DOCUMENT', label: 'Company registration document' },
  { value: 'PASSPORT_FRONT', label: 'Passport photo page' },
  { value: 'DRIVERS_LICENSE_FRONT', label: "Driver’s license — front" },
  { value: 'DRIVERS_LICENSE_BACK', label: "Driver’s license — back" },
  { value: 'NATIONAL_ID_FRONT', label: 'National identity card — front' },
  { value: 'NATIONAL_ID_BACK', label: 'National identity card — back' },
  { value: 'PERSON_PURPORTING_TO_ACT_AUTHORISATION_LETTER', label: 'Authorization letter' },
];
