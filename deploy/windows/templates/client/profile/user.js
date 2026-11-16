// 柠檬账客户端 · 浏览器预置偏好
// 放在 profile 目录下，Firefox 启动时自动读取，用于关闭首次运行引导、
// 默认浏览器提示、遥测上报和自动更新（客户环境通常不允许外网）。
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("startup.homepage_welcome_url", "");
user_pref("startup.homepage_welcome_url.additional", "");
user_pref("browser.startup.page", 0);
user_pref("browser.newtabpage.enabled", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("datareporting.healthreport.uploadEnabled", false);
user_pref("toolkit.telemetry.enabled", false);
user_pref("toolkit.telemetry.unified", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("app.update.enabled", false);
user_pref("app.update.auto", false);
user_pref("browser.search.update", false);
user_pref("extensions.update.enabled", false);
user_pref("network.cookie.cookieBehavior", 0);
user_pref("browser.tabs.warnOnClose", false);
