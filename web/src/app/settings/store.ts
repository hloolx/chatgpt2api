"use client";

import { create } from "zustand";
import { toast } from "sonner";

import {
  cleanupImageStorage,
  cleanupLogs,
  createCPAPool,
  deleteCPAPool,
  fetchCPAPoolFiles,
  fetchCPAPools,
  fetchLogGovernance,
  fetchImageStorageGovernance,
  fetchRegisterConfig,
  resetRegister as resetRegisterApi,
  fetchSettingsConfig,
  startRegister,
  startCPAImport,
  stopRegister,
  updateCPAPool,
  updateLoginPageImageSettings,
  updateRegisterConfig,
  updateSettingsConfig,
  type BillingPeriod,
  type BillingType,
  type CPAPool,
  type CPARemoteFile,
  type ImageStorageCleanupResult,
  type ImageStorageGovernanceSummary,
  type ImageVisibility,
  type LogCleanupResult,
  type LogGovernanceSummary,
  type LoginPageImageSettings,
  type RegisterConfig,
  type SettingsConfig,
} from "@/lib/api";
import { dispatchAppMetaUpdated } from "@/lib/app-meta";
import {
  LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM,
  normalizeLoginPageImageMode,
  normalizeLoginPageImageTransform,
  type LoginPageImageMode,
} from "@/lib/login-page-image-layout";

export const PAGE_SIZE_OPTIONS = ["50", "100", "200"] as const;

export type PageSizeOption = (typeof PAGE_SIZE_OPTIONS)[number];

function normalizeDefaultBillingType(value: unknown): BillingType {
  return value === "subscription" ? "subscription" : "standard";
}

function normalizeDefaultSubscriptionPeriod(value: unknown): BillingPeriod {
  if (value === "daily" || value === "weekly" || value === "monthly") {
    return value;
  }
  return "monthly";
}

function normalizeImageVisibility(value: unknown): ImageVisibility {
  return value === "public" ? "public" : "private";
}

function normalizeConfig(config: SettingsConfig): SettingsConfig {
  const loginImageTransform = normalizeLoginPageImageTransform({
    zoom: Number(config.login_page_image_zoom),
    positionX: Number(config.login_page_image_position_x),
    positionY: Number(config.login_page_image_position_y),
  });
  return {
    ...config,
    refresh_account_interval_minute: Number(config.refresh_account_interval_minute || 5),
    image_task_timeout_seconds: Number(config.image_task_timeout_seconds || 300),
    user_default_concurrent_limit: Number(config.user_default_concurrent_limit || 0),
    user_default_rpm_limit: Number(config.user_default_rpm_limit || 0),
    default_billing_type: normalizeDefaultBillingType(config.default_billing_type),
    default_standard_balance: Math.max(0, Number(config.default_standard_balance) || 0),
    default_subscription_quota: Math.max(0, Number(config.default_subscription_quota) || 0),
    default_subscription_period: normalizeDefaultSubscriptionPeriod(config.default_subscription_period),
    image_retention_days: Number(config.image_retention_days || 30),
    image_storage_limit_mb: Math.max(0, Number(config.image_storage_limit_mb) || 0),
    force_image_url_response: Boolean(config.force_image_url_response),
    default_image_visibility: normalizeImageVisibility(config.default_image_visibility),
    log_retention_days: Number(config.log_retention_days || 7),
    auto_remove_invalid_accounts: Boolean(config.auto_remove_invalid_accounts),
    auto_remove_rate_limited_accounts: Boolean(config.auto_remove_rate_limited_accounts),
    log_levels: Array.isArray(config.log_levels) ? config.log_levels : [],
    proxy: typeof config.proxy === "string" ? config.proxy : "",
    base_url: typeof config.base_url === "string" ? config.base_url : "",
    registration_enabled: Boolean(config.registration_enabled),
    linuxdo_enabled: Boolean(config.linuxdo_enabled),
    linuxdo_client_id: typeof config.linuxdo_client_id === "string" ? config.linuxdo_client_id : "",
    linuxdo_client_secret: "",
    linuxdo_client_secret_configured: Boolean(config.linuxdo_client_secret_configured),
    linuxdo_redirect_url: typeof config.linuxdo_redirect_url === "string" ? config.linuxdo_redirect_url : "",
    linuxdo_frontend_redirect_url:
      typeof config.linuxdo_frontend_redirect_url === "string" ? config.linuxdo_frontend_redirect_url : "/auth/linuxdo/callback",
    update_repo: typeof config.update_repo === "string" ? config.update_repo : "ZyphrZero/chatgpt2api",
    update_github_token: "",
    update_github_token_configured: Boolean(config.update_github_token_configured),
    login_page_image_url: typeof config.login_page_image_url === "string" ? config.login_page_image_url : "",
    login_page_image_mode: normalizeLoginPageImageMode(config.login_page_image_mode),
    login_page_image_zoom: loginImageTransform.zoom,
    login_page_image_position_x: loginImageTransform.positionX,
    login_page_image_position_y: loginImageTransform.positionY,
    cloud_storage_enabled: Boolean(config.cloud_storage_enabled),
    cloud_storage_uploader: typeof config.cloud_storage_uploader === "string" ? config.cloud_storage_uploader : "auto",
    cloud_proxy: typeof config.cloud_proxy === "string" ? config.cloud_proxy : "",
    cloud_proxy_enabled: Boolean(config.cloud_proxy_enabled ?? true),
    s3_endpoint: typeof config.s3_endpoint === "string" ? config.s3_endpoint : "",
    s3_region: typeof config.s3_region === "string" ? config.s3_region : "auto",
    s3_access_key_id: typeof config.s3_access_key_id === "string" ? config.s3_access_key_id : "",
    s3_secret_access_key: "",
    s3_secret_access_key_configured: Boolean(config.s3_secret_access_key_configured),
    s3_bucket: typeof config.s3_bucket === "string" ? config.s3_bucket : "",
    s3_public_url: typeof config.s3_public_url === "string" ? config.s3_public_url : "",
    s3_path_prefix: typeof config.s3_path_prefix === "string" ? config.s3_path_prefix : "",
    s3_force_path_style: Boolean(config.s3_force_path_style),
  };
}

function normalizeFiles(items: CPARemoteFile[]) {
  const seen = new Set<string>();
  const files: CPARemoteFile[] = [];
  for (const item of items) {
    const name = String(item.name || "").trim();
    if (!name || seen.has(name)) {
      continue;
    }
    seen.add(name);
    files.push({
      name,
      email: String(item.email || "").trim(),
    });
  }
  return files;
}

type SettingsStore = {
  config: SettingsConfig | null;
  isLoadingConfig: boolean;
  isSavingConfig: boolean;
  logGovernance: LogGovernanceSummary | null;
  lastLogCleanup: LogCleanupResult | null;
  isLoadingLogGovernance: boolean;
  isCleaningLogs: boolean;
  imageStorageGovernance: ImageStorageGovernanceSummary | null;
  lastImageStorageCleanup: ImageStorageCleanupResult | null;
  isLoadingImageStorageGovernance: boolean;
  isCleaningImageStorage: boolean;

  registerConfig: RegisterConfig | null;
  isLoadingRegister: boolean;
  isSavingRegister: boolean;

  pools: CPAPool[];
  isLoadingPools: boolean;
  deletingId: string | null;
  loadingFilesId: string | null;

  dialogOpen: boolean;
  editingPool: CPAPool | null;
  formName: string;
  formBaseUrl: string;
  formSecretKey: string;
  showSecret: boolean;
  isSavingPool: boolean;

  browserOpen: boolean;
  browserPool: CPAPool | null;
  remoteFiles: CPARemoteFile[];
  selectedNames: string[];
  fileQuery: string;
  filePage: number;
  pageSize: PageSizeOption;
  isStartingImport: boolean;

  initialize: () => Promise<void>;
  loadConfig: () => Promise<void>;
  saveConfig: () => Promise<void>;
  setRefreshAccountIntervalMinute: (value: string) => void;
  setImageTaskTimeoutSeconds: (value: string) => void;
  setUserDefaultConcurrentLimit: (value: string) => void;
  setUserDefaultRpmLimit: (value: string) => void;
  setDefaultBillingType: (value: BillingType) => void;
  setDefaultStandardBalance: (value: string) => void;
  setDefaultSubscriptionQuota: (value: string) => void;
  setDefaultSubscriptionPeriod: (value: BillingPeriod) => void;
  setImageRetentionDays: (value: string) => void;
  setImageStorageLimitMb: (value: string) => void;
  setForceImageUrlResponse: (value: boolean) => void;
  setDefaultImageVisibility: (value: ImageVisibility) => void;
  setLogRetentionDays: (value: string) => void;
  setAutoRemoveInvalidAccounts: (value: boolean) => void;
  setAutoRemoveRateLimitedAccounts: (value: boolean) => void;
  setLogLevel: (level: string, enabled: boolean) => void;
  setProxy: (value: string) => void;
  setBaseUrl: (value: string) => void;
  setRegistrationEnabled: (value: boolean) => void;
  setCloudStorageEnabled: (value: boolean) => void;
  setCloudStorageUploader: (value: string) => void;
  setS3Endpoint: (value: string) => void;
  setS3Region: (value: string) => void;
  setS3AccessKeyID: (value: string) => void;
  setS3SecretAccessKey: (value: string) => void;
  setS3Bucket: (value: string) => void;
  setS3PublicURL: (value: string) => void;
  setS3PathPrefix: (value: string) => void;
  setS3ForcePathStyle: (value: boolean) => void;
  setCloudProxy: (value: string) => void;
  setCloudProxyEnabled: (value: boolean) => void;
  setLinuxDoEnabled: (value: boolean) => void;
  setLinuxDoClientId: (value: string) => void;
  setLinuxDoClientSecret: (value: string) => void;
  setLinuxDoRedirectUrl: (value: string) => void;
  setLinuxDoFrontendRedirectUrl: (value: string) => void;
  setUpdateRepo: (value: string) => void;
  setUpdateGitHubToken: (value: string) => void;
  setLoginPageImageUrl: (value: string) => void;
  setLoginPageImageMode: (value: LoginPageImageMode) => void;
  setLoginPageImageTransform: (transform: { zoom: number; positionX: number; positionY: number }) => void;
  restoreDefaultLoginPageImage: () => void;
  saveLoginPageImage: (options: { file?: File | null; action: "keep" | "replace" | "remove" }) => Promise<boolean>;
  loadLogGovernance: (silent?: boolean) => Promise<void>;
  cleanupLogsByRetention: () => Promise<void>;
  loadImageStorageGovernance: (silent?: boolean) => Promise<void>;
  cleanupImageStorageByRetention: () => Promise<void>;
  cleanupImageStorageByQuota: (includePublic?: boolean) => Promise<void>;
  cleanupImageThumbnails: () => Promise<void>;

  loadRegister: (silent?: boolean) => Promise<void>;
  setRegisterConfig: (config: RegisterConfig) => void;
  setRegisterProxy: (value: string) => void;
  setRegisterTotal: (value: string) => void;
  setRegisterThreads: (value: string) => void;
  setRegisterMode: (value: "total" | "quota" | "available") => void;
  setRegisterTargetQuota: (value: string) => void;
  setRegisterTargetAvailable: (value: string) => void;
  setRegisterCheckInterval: (value: string) => void;
  setRegisterMailField: (key: "request_timeout" | "wait_timeout" | "wait_interval", value: string) => void;
  addRegisterProvider: () => void;
  updateRegisterProvider: (index: number, updates: Record<string, unknown>) => void;
  deleteRegisterProvider: (index: number) => void;
  saveRegister: () => Promise<void>;
  toggleRegister: () => Promise<void>;
  resetRegister: () => Promise<void>;

  loadPools: (silent?: boolean) => Promise<void>;
  openAddDialog: () => void;
  openEditDialog: (pool: CPAPool) => void;
  setDialogOpen: (open: boolean) => void;
  setFormName: (value: string) => void;
  setFormBaseUrl: (value: string) => void;
  setFormSecretKey: (value: string) => void;
  setShowSecret: (checked: boolean) => void;
  savePool: () => Promise<void>;
  deletePool: (pool: CPAPool) => Promise<void>;

  browseFiles: (pool: CPAPool) => Promise<void>;
  setBrowserOpen: (open: boolean) => void;
  toggleFile: (name: string, checked: boolean) => void;
  replaceSelectedNames: (names: string[]) => void;
  setFileQuery: (value: string) => void;
  setFilePage: (page: number) => void;
  setPageSize: (value: PageSizeOption) => void;
  startImport: () => Promise<void>;
};

export const useSettingsStore = create<SettingsStore>((set, get) => ({
  config: null,
  isLoadingConfig: true,
  isSavingConfig: false,
  logGovernance: null,
  lastLogCleanup: null,
  isLoadingLogGovernance: true,
  isCleaningLogs: false,
  imageStorageGovernance: null,
  lastImageStorageCleanup: null,
  isLoadingImageStorageGovernance: true,
  isCleaningImageStorage: false,

  registerConfig: null,
  isLoadingRegister: true,
  isSavingRegister: false,

  pools: [],
  isLoadingPools: true,
  deletingId: null,
  loadingFilesId: null,

  dialogOpen: false,
  editingPool: null,
  formName: "",
  formBaseUrl: "",
  formSecretKey: "",
  showSecret: false,
  isSavingPool: false,

  browserOpen: false,
  browserPool: null,
  remoteFiles: [],
  selectedNames: [],
  fileQuery: "",
  filePage: 1,
  pageSize: "100",
  isStartingImport: false,

  initialize: async () => {
    await Promise.allSettled([get().loadConfig(), get().loadPools(), get().loadLogGovernance(), get().loadImageStorageGovernance()]);
  },

  loadConfig: async () => {
    set({ isLoadingConfig: true });
    try {
      const data = await fetchSettingsConfig();
      set({
        config: normalizeConfig(data.config),
      });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "加载系统配置失败");
    } finally {
      set({ isLoadingConfig: false });
    }
  },

  saveConfig: async () => {
    const { config } = get();
    if (!config) {
      return;
    }

    set({ isSavingConfig: true });
    try {
      const linuxDoClientSecret = String(config.linuxdo_client_secret || "").trim();
      const updateGitHubToken = String(config.update_github_token || "").trim();
      const payload: SettingsConfig = {
        ...config,
        refresh_account_interval_minute: Math.max(1, Number(config.refresh_account_interval_minute) || 1),
        image_task_timeout_seconds: Math.min(3600, Math.max(30, Number(config.image_task_timeout_seconds) || 300)),
        user_default_concurrent_limit: Math.max(0, Number(config.user_default_concurrent_limit) || 0),
        user_default_rpm_limit: Math.max(0, Number(config.user_default_rpm_limit) || 0),
        default_billing_type: normalizeDefaultBillingType(config.default_billing_type),
        default_standard_balance: Math.max(0, Number(config.default_standard_balance) || 0),
        default_subscription_quota: Math.max(0, Number(config.default_subscription_quota) || 0),
        default_subscription_period: normalizeDefaultSubscriptionPeriod(config.default_subscription_period),
        image_retention_days: Math.max(1, Number(config.image_retention_days) || 30),
        image_storage_limit_mb: Math.max(0, Number(config.image_storage_limit_mb) || 0),
        force_image_url_response: Boolean(config.force_image_url_response),
        default_image_visibility: normalizeImageVisibility(config.default_image_visibility),
        log_retention_days: Math.min(3650, Math.max(1, Number(config.log_retention_days) || 7)),
        auto_remove_invalid_accounts: Boolean(config.auto_remove_invalid_accounts),
        auto_remove_rate_limited_accounts: Boolean(config.auto_remove_rate_limited_accounts),
        proxy: config.proxy.trim(),
        base_url: String(config.base_url || "").trim(),
        registration_enabled: Boolean(config.registration_enabled),
        linuxdo_enabled: Boolean(config.linuxdo_enabled),
        linuxdo_client_id: String(config.linuxdo_client_id || "").trim(),
        linuxdo_client_secret: linuxDoClientSecret,
        linuxdo_redirect_url: String(config.linuxdo_redirect_url || "").trim(),
        linuxdo_frontend_redirect_url: String(config.linuxdo_frontend_redirect_url || "").trim(),
        update_repo: String(config.update_repo ?? "ZyphrZero/chatgpt2api").trim(),
        update_github_token: updateGitHubToken,
      };
      if (!linuxDoClientSecret) {
        delete payload.linuxdo_client_secret;
      }
      if (!updateGitHubToken) {
        delete payload.update_github_token;
      }
      delete payload.linuxdo_client_secret_configured;
      delete payload.update_github_token_configured;

      const data = await updateSettingsConfig(payload);
      const nextConfig = normalizeConfig(data.config);
      set({
        config: nextConfig,
      });
      dispatchAppMetaUpdated({
        default_image_visibility: normalizeImageVisibility(nextConfig.default_image_visibility),
      });
      toast.success("配置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存系统配置失败");
    } finally {
      set({ isSavingConfig: false });
    }
  },

  setRefreshAccountIntervalMinute: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          refresh_account_interval_minute: value,
        },
      };
    });
  },

  setImageRetentionDays: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_retention_days: value } } : {});
  },

  setImageStorageLimitMb: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_storage_limit_mb: value } } : {});
  },

  setForceImageUrlResponse: (value) => {
    set((state) => state.config ? { config: { ...state.config, force_image_url_response: value } } : {});
  },

  setDefaultImageVisibility: (value) => {
    set((state) => state.config ? { config: { ...state.config, default_image_visibility: normalizeImageVisibility(value) } } : {});
  },

  setLogRetentionDays: (value) => {
    set((state) => state.config ? { config: { ...state.config, log_retention_days: value } } : {});
  },

  setImageTaskTimeoutSeconds: (value) => {
    set((state) => state.config ? { config: { ...state.config, image_task_timeout_seconds: value } } : {});
  },

  setUserDefaultConcurrentLimit: (value) => {
    set((state) => state.config ? { config: { ...state.config, user_default_concurrent_limit: value } } : {});
  },

  setUserDefaultRpmLimit: (value) => {
    set((state) => state.config ? { config: { ...state.config, user_default_rpm_limit: value } } : {});
  },

  setDefaultBillingType: (value) => {
    set((state) => state.config ? { config: { ...state.config, default_billing_type: value } } : {});
  },

  setDefaultStandardBalance: (value) => {
    set((state) => state.config ? { config: { ...state.config, default_standard_balance: value } } : {});
  },

  setDefaultSubscriptionQuota: (value) => {
    set((state) => state.config ? { config: { ...state.config, default_subscription_quota: value } } : {});
  },

  setDefaultSubscriptionPeriod: (value) => {
    set((state) => state.config ? { config: { ...state.config, default_subscription_period: value } } : {});
  },

  setAutoRemoveInvalidAccounts: (value) => {
    set((state) => state.config ? { config: { ...state.config, auto_remove_invalid_accounts: value } } : {});
  },

  setAutoRemoveRateLimitedAccounts: (value) => {
    set((state) => state.config ? { config: { ...state.config, auto_remove_rate_limited_accounts: value } } : {});
  },

  setLogLevel: (level, enabled) => {
    set((state) => {
      if (!state.config) return {};
      const levels = new Set(state.config.log_levels || []);
      if (enabled) levels.add(level);
      else levels.delete(level);
      return { config: { ...state.config, log_levels: Array.from(levels) } };
    });
  },

  setProxy: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          proxy: value,
        },
      };
    });
  },

  setBaseUrl: (value) => {
    set((state) => {
      if (!state.config) {
        return {};
      }
      return {
        config: {
          ...state.config,
          base_url: value,
        },
      };
    });
  },

  setRegistrationEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, registration_enabled: value } } : {});
  },

  setLinuxDoEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_enabled: value } } : {});
  },

  setLinuxDoClientId: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_client_id: value } } : {});
  },

  setLinuxDoClientSecret: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_client_secret: value } } : {});
  },

  setLinuxDoRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_redirect_url: value } } : {});
  },

  setLinuxDoFrontendRedirectUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, linuxdo_frontend_redirect_url: value } } : {});
  },

  setUpdateRepo: (value) => {
    set((state) => state.config ? { config: { ...state.config, update_repo: value } } : {});
  },

  setUpdateGitHubToken: (value) => {
    set((state) => state.config ? { config: { ...state.config, update_github_token: value } } : {});
  },

  setCloudStorageEnabled: (value) => {
    set((state) => {
      if (!state.config) return state;
      return { config: { ...state.config, cloud_storage_enabled: value } };
    });
  },

  setCloudStorageUploader: (value) => {
    set((state) => {
      if (!state.config) return state;
      return { config: { ...state.config, cloud_storage_uploader: value } };
    });
  },

  setS3Endpoint: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_endpoint: value } } : {});
  },
  setS3Region: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_region: value } } : {});
  },
  setS3AccessKeyID: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_access_key_id: value } } : {});
  },
  setS3SecretAccessKey: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_secret_access_key: value } } : {});
  },
  setS3Bucket: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_bucket: value } } : {});
  },
  setS3PublicURL: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_public_url: value } } : {});
  },
  setS3PathPrefix: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_path_prefix: value } } : {});
  },
  setS3ForcePathStyle: (value) => {
    set((state) => state.config ? { config: { ...state.config, s3_force_path_style: value } } : {});
  },
  setCloudProxy: (value) => {
    set((state) => state.config ? { config: { ...state.config, cloud_proxy: value } } : {});
  },
  setCloudProxyEnabled: (value) => {
    set((state) => state.config ? { config: { ...state.config, cloud_proxy_enabled: value } } : {});
  },

  setLoginPageImageUrl: (value) => {
    set((state) => state.config ? { config: { ...state.config, login_page_image_url: value } } : {});
  },

  setLoginPageImageMode: (value) => {
    set((state) => state.config ? { config: { ...state.config, login_page_image_mode: value } } : {});
  },

  setLoginPageImageTransform: (transform) => {
    const normalized = normalizeLoginPageImageTransform(transform);
    set((state) => state.config ? {
      config: {
        ...state.config,
        login_page_image_zoom: normalized.zoom,
        login_page_image_position_x: normalized.positionX,
        login_page_image_position_y: normalized.positionY,
      },
    } : {});
  },

  restoreDefaultLoginPageImage: () => {
    set((state) => state.config ? {
      config: {
        ...state.config,
        login_page_image_url: "",
        login_page_image_zoom: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.zoom,
        login_page_image_position_x: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionX,
        login_page_image_position_y: LOGIN_PAGE_IMAGE_DEFAULT_TRANSFORM.positionY,
      },
    } : {});
  },

  saveLoginPageImage: async ({ file, action }) => {
    const { config } = get();
    if (!config) {
      return false;
    }
    const transform = normalizeLoginPageImageTransform({
      zoom: Number(config.login_page_image_zoom),
      positionX: Number(config.login_page_image_position_x),
      positionY: Number(config.login_page_image_position_y),
    });
    const settings: LoginPageImageSettings = {
      login_page_image_url: String(config.login_page_image_url || "").trim(),
      login_page_image_mode: normalizeLoginPageImageMode(config.login_page_image_mode),
      login_page_image_zoom: transform.zoom,
      login_page_image_position_x: transform.positionX,
      login_page_image_position_y: transform.positionY,
    };

    set({ isSavingConfig: true });
    try {
      const data = await updateLoginPageImageSettings(settings, { action, file });
      const nextConfig = normalizeConfig(data.config);
      set({ config: nextConfig });
      dispatchAppMetaUpdated({
        app_title: "chatgpt2api",
        project_name: "chatgpt2api",
        login_page_image_url: String(nextConfig.login_page_image_url || ""),
        login_page_image_mode: normalizeLoginPageImageMode(nextConfig.login_page_image_mode),
        login_page_image_zoom: Number(nextConfig.login_page_image_zoom),
        login_page_image_position_x: Number(nextConfig.login_page_image_position_x),
        login_page_image_position_y: Number(nextConfig.login_page_image_position_y),
      });
      toast.success("登录页图片已保存");
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存登录页图片失败");
      return false;
    } finally {
      set({ isSavingConfig: false });
    }
  },

  loadLogGovernance: async (silent = false) => {
    if (!silent) set({ isLoadingLogGovernance: true });
    try {
      const data = await fetchLogGovernance();
      set({ logGovernance: data.governance });
    } catch (error) {
      if (!silent) toast.error(error instanceof Error ? error.message : "加载日志治理数据失败");
    } finally {
      if (!silent) set({ isLoadingLogGovernance: false });
    }
  },

  cleanupLogsByRetention: async () => {
    const { config } = get();
    if (!config) {
      return;
    }
    const retentionDays = Math.min(3650, Math.max(1, Number(config.log_retention_days) || 7));
    set({ isCleaningLogs: true });
    try {
      const data = await cleanupLogs(retentionDays);
      set({
        lastLogCleanup: data.cleanup,
        logGovernance: data.governance,
      });
      toast.success(`已清理 ${data.cleanup.deleted} 条历史日志`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "清理日志失败");
    } finally {
      set({ isCleaningLogs: false });
    }
  },

  loadImageStorageGovernance: async (silent = false) => {
    if (!silent) set({ isLoadingImageStorageGovernance: true });
    try {
      const data = await fetchImageStorageGovernance();
      set({ imageStorageGovernance: data.governance });
    } catch (error) {
      if (!silent) toast.error(error instanceof Error ? error.message : "加载图片存储数据失败");
    } finally {
      if (!silent) set({ isLoadingImageStorageGovernance: false });
    }
  },

  cleanupImageStorageByRetention: async () => {
    const { config } = get();
    if (!config) return;
    const retentionDays = Math.max(1, Number(config.image_retention_days) || 30);
    set({ isCleaningImageStorage: true });
    try {
      const data = await cleanupImageStorage({ action: "retention", retention_days: retentionDays });
      set({ lastImageStorageCleanup: data.cleanup, imageStorageGovernance: data.governance });
      toast.success(`已清理 ${data.cleanup.deleted_images} 张过期图片`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "清理图片失败");
    } finally {
      set({ isCleaningImageStorage: false });
    }
  },

  cleanupImageStorageByQuota: async (includePublic = false) => {
    const { config } = get();
    if (!config) return;
    const maxMb = Math.max(0, Number(config.image_storage_limit_mb) || 0);
    if (maxMb <= 0) {
      toast.error("请先设置图片容量上限");
      return;
    }
    set({ isCleaningImageStorage: true });
    try {
      const data = await cleanupImageStorage({ action: "quota", max_mb: maxMb, include_public: includePublic });
      set({ lastImageStorageCleanup: data.cleanup, imageStorageGovernance: data.governance });
      toast.success(`已按容量清理 ${data.cleanup.deleted_images} 张图片`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "按容量清理图片失败");
    } finally {
      set({ isCleaningImageStorage: false });
    }
  },

  cleanupImageThumbnails: async () => {
    set({ isCleaningImageStorage: true });
    try {
      const data = await cleanupImageStorage({ action: "thumbnails" });
      set({ lastImageStorageCleanup: data.cleanup, imageStorageGovernance: data.governance });
      toast.success(`已清理 ${data.cleanup.deleted_thumbnails} 个缩略图缓存`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "清理缩略图失败");
    } finally {
      set({ isCleaningImageStorage: false });
    }
  },

  loadRegister: async (silent = false) => {
    if (!silent) set({ isLoadingRegister: true });
    try {
      const data = await fetchRegisterConfig();
      set({ registerConfig: data.register });
    } catch (error) {
      if (!silent) toast.error(error instanceof Error ? error.message : "加载注册配置失败");
    } finally {
      if (!silent) set({ isLoadingRegister: false });
    }
  },

  setRegisterConfig: (config) => {
    set({ registerConfig: config, isLoadingRegister: false });
  },

  setRegisterProxy: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, proxy: value } } : {});
  },

  setRegisterTotal: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, total: Number(value) || 0 } } : {});
  },

  setRegisterThreads: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, threads: Number(value) || 0 } } : {});
  },

  setRegisterMode: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, mode: value } } : {});
  },

  setRegisterTargetQuota: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, target_quota: Number(value) || 0 } } : {});
  },

  setRegisterTargetAvailable: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, target_available: Number(value) || 0 } } : {});
  },

  setRegisterCheckInterval: (value) => {
    set((state) => state.registerConfig ? { registerConfig: { ...state.registerConfig, check_interval: Number(value) || 0 } } : {});
  },

  setRegisterMailField: (key, value) => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: { ...state.registerConfig.mail, [key]: Number(value) || 0 },
      },
    } : {});
  },

  addRegisterProvider: () => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: {
          ...state.registerConfig.mail,
          providers: [
            ...(state.registerConfig.mail.providers || []),
            { enable: true, type: "tempmail_lol", api_key: "", domain: [] },
          ],
        },
      },
    } : {});
  },

  updateRegisterProvider: (index, updates) => {
    set((state) => {
      if (!state.registerConfig) return {};
      const providers = [...(state.registerConfig.mail.providers || [])];
      providers[index] = { ...providers[index], ...updates };
      return { registerConfig: { ...state.registerConfig, mail: { ...state.registerConfig.mail, providers } } };
    });
  },

  deleteRegisterProvider: (index) => {
    set((state) => state.registerConfig ? {
      registerConfig: {
        ...state.registerConfig,
        mail: {
          ...state.registerConfig.mail,
          providers: (state.registerConfig.mail.providers || []).filter((_, itemIndex) => itemIndex !== index),
        },
      },
    } : {});
  },

  saveRegister: async () => {
    const { registerConfig } = get();
    if (!registerConfig) return;
    try {
      set({ isSavingRegister: true });
      const data = await updateRegisterConfig({
        mail: registerConfig.mail,
        proxy: registerConfig.proxy.trim(),
        total: Math.max(1, Number(registerConfig.total) || 1),
        threads: Math.max(1, Number(registerConfig.threads) || 1),
        mode: registerConfig.mode,
        target_quota: Math.max(1, Number(registerConfig.target_quota) || 1),
        target_available: Math.max(1, Number(registerConfig.target_available) || 1),
        check_interval: Math.max(1, Number(registerConfig.check_interval) || 5),
      });
      set({ registerConfig: data.register });
      toast.success("注册配置已保存");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存注册配置失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  toggleRegister: async () => {
    const { registerConfig } = get();
    if (!registerConfig) return;
    set({ isSavingRegister: true });
    try {
      if (!registerConfig.enabled) {
        await updateRegisterConfig({
          mail: registerConfig.mail,
          proxy: registerConfig.proxy.trim(),
          total: Math.max(1, Number(registerConfig.total) || 1),
          threads: Math.max(1, Number(registerConfig.threads) || 1),
          mode: registerConfig.mode,
          target_quota: Math.max(1, Number(registerConfig.target_quota) || 1),
          target_available: Math.max(1, Number(registerConfig.target_available) || 1),
          check_interval: Math.max(1, Number(registerConfig.check_interval) || 5),
        });
      }
      const data = registerConfig.enabled ? await stopRegister() : await startRegister();
      set({ registerConfig: data.register });
      toast.success(registerConfig.enabled ? "注册任务已停止" : "注册任务已启动");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "切换注册状态失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  resetRegister: async () => {
    set({ isSavingRegister: true });
    try {
      const data = await resetRegisterApi();
      set({ registerConfig: data.register });
      toast.success("注册统计已重置");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "重置注册统计失败");
    } finally {
      set({ isSavingRegister: false });
    }
  },

  loadPools: async (silent = false) => {
    if (!silent) {
      set({ isLoadingPools: true });
    }
    try {
      const data = await fetchCPAPools();
      set({ pools: data.pools });
    } catch (error) {
      if (!silent) {
        toast.error(error instanceof Error ? error.message : "加载 CPA 连接失败");
      }
    } finally {
      if (!silent) {
        set({ isLoadingPools: false });
      }
    }
  },

  openAddDialog: () => {
    set({
      editingPool: null,
      formName: "",
      formBaseUrl: "",
      formSecretKey: "",
      showSecret: false,
      dialogOpen: true,
    });
  },

  openEditDialog: (pool) => {
    set({
      editingPool: pool,
      formName: pool.name,
      formBaseUrl: pool.base_url,
      formSecretKey: "",
      showSecret: false,
      dialogOpen: true,
    });
  },

  setDialogOpen: (open) => {
    set({ dialogOpen: open });
  },

  setFormName: (value) => {
    set({ formName: value });
  },

  setFormBaseUrl: (value) => {
    set({ formBaseUrl: value });
  },

  setFormSecretKey: (value) => {
    set({ formSecretKey: value });
  },

  setShowSecret: (checked) => {
    set({ showSecret: checked });
  },

  savePool: async () => {
    const { editingPool, formName, formBaseUrl, formSecretKey } = get();
    if (!formBaseUrl.trim()) {
      toast.error("请输入 CPA 地址");
      return;
    }
    if (!editingPool && !formSecretKey.trim()) {
      toast.error("请输入 Secret Key");
      return;
    }

    set({ isSavingPool: true });
    try {
      if (editingPool) {
        const data = await updateCPAPool(editingPool.id, {
          name: formName.trim(),
          base_url: formBaseUrl.trim(),
          secret_key: formSecretKey.trim() || undefined,
        });
        set({ pools: data.pools, dialogOpen: false });
        toast.success("连接已更新");
      } else {
        const data = await createCPAPool({
          name: formName.trim(),
          base_url: formBaseUrl.trim(),
          secret_key: formSecretKey.trim(),
        });
        set({ pools: data.pools, dialogOpen: false });
        toast.success("连接已添加");
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "保存失败");
    } finally {
      set({ isSavingPool: false });
    }
  },

  deletePool: async (pool) => {
    set({ deletingId: pool.id });
    try {
      const data = await deleteCPAPool(pool.id);
      set({ pools: data.pools });
      toast.success("连接已删除");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除失败");
    } finally {
      set({ deletingId: null });
    }
  },

  browseFiles: async (pool) => {
    set({ loadingFilesId: pool.id });
    try {
      const data = await fetchCPAPoolFiles(pool.id);
      const files = normalizeFiles(data.files);
      set({
        browserPool: pool,
        remoteFiles: files,
        selectedNames: [],
        fileQuery: "",
        filePage: 1,
        browserOpen: true,
      });
      toast.success(`读取成功，共 ${files.length} 个远程账号`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "读取远程账号失败");
    } finally {
      set({ loadingFilesId: null });
    }
  },

  setBrowserOpen: (open) => {
    set({ browserOpen: open });
  },

  toggleFile: (name, checked) => {
    set((state) => {
      if (checked) {
        return {
          selectedNames: Array.from(new Set([...state.selectedNames, name])),
        };
      }
      return {
        selectedNames: state.selectedNames.filter((item) => item !== name),
      };
    });
  },

  replaceSelectedNames: (names) => {
    set({ selectedNames: Array.from(new Set(names)) });
  },

  setFileQuery: (value) => {
    set({ fileQuery: value, filePage: 1 });
  },

  setFilePage: (page) => {
    set({ filePage: page });
  },

  setPageSize: (value) => {
    set({ pageSize: value, filePage: 1 });
  },

  startImport: async () => {
    const { browserPool, selectedNames, pools } = get();
    if (!browserPool) {
      return;
    }
    if (selectedNames.length === 0) {
      toast.error("请先选择要导入的账号");
      return;
    }

    set({ isStartingImport: true });
    try {
      const result = await startCPAImport(browserPool.id, selectedNames);
      set({
        pools: pools.map((pool) =>
          pool.id === browserPool.id ? { ...pool, import_job: result.import_job } : pool,
        ),
        browserOpen: false,
      });
      toast.success("导入任务已启动");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "启动导入失败");
    } finally {
      set({ isStartingImport: false });
    }
  },
}));
