import { useState, useCallback } from 'react';

export interface OnboardingData {
  firstName: string;
  lastName: string;
  email: string;
  password: string;
  companyName: string;
  complianceFlags: string[];
  legalName: string;
  country: string;
  phone: string;
  website: string;
  ein: string;
  industry: string;
  companyType: string;
}

export interface OnboardingState {
  currentStep: number;
  totalSteps: number;
  data: OnboardingData;
  completed: boolean;
}

const TOTAL_STEPS = 6;

const initialData: OnboardingData = {
  firstName: '',
  lastName: '',
  email: '',
  password: '',
  companyName: '',
  complianceFlags: [],
  legalName: '',
  country: '',
  phone: '',
  website: '',
  ein: '',
  industry: '',
  companyType: '',
};

export function useOnboarding() {
  const [state, setState] = useState<OnboardingState>({
    currentStep: 1,
    totalSteps: TOTAL_STEPS,
    data: { ...initialData },
    completed: false,
  });

  const updateData = useCallback((patch: Partial<OnboardingData>) => {
    setState((prev) => ({
      ...prev,
      data: { ...prev.data, ...patch },
    }));
  }, []);

  const nextStep = useCallback(() => {
    setState((prev) => {
      if (prev.currentStep >= TOTAL_STEPS) {
        return { ...prev, completed: true };
      }
      return { ...prev, currentStep: prev.currentStep + 1 };
    });
  }, []);

  const prevStep = useCallback(() => {
    setState((prev) => ({
      ...prev,
      currentStep: Math.max(1, prev.currentStep - 1),
    }));
  }, []);

  return { state, updateData, nextStep, prevStep };
}
