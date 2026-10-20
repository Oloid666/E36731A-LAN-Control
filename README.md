# E36731A LAN Control v0.3.1

Windows 10/11 x64 本機 HTML 儀器控制程式。透過 LAN / TCP 5025 傳送 SCPI；使用者不需安裝 Python、Go、PyVISA 或 Keysight VISA。預設 IP 為 **169.254.30.1**。

[下載 Windows 免安裝包 v0.3.1](https://github.com/Oloid666/E36731A-LAN-Control/raw/refs/heads/main/downloads/E36731A-LAN-Windows-x64-v0.3.1.zip)

**狀態：46 項控制／通訊／保存設定測試、Go race 檢查、go vet 與 JavaScript 邏輯檢查通過，Windows x64 EXE 已交叉編譯。v0.3.1 尚未在實機或 Windows 上驗證，也未完成真實瀏覽器視覺驗證。** 替身測試不代表儀器接受指令、保護時序或動態性能已驗證。

## 一般使用

1. 先用儀器面板確認 OFF，關閉所有舊版本 EXE；解壓縮新 ZIP 到獨立資料夾。
2. 雙擊 `E36731A-LAN-Control.exe`，保留程式視窗。瀏覽器會開啟；否則複製程式視窗的完整本機網址。
3. 預填儀器 IP `169.254.30.1`，按「連線」。電腦需能連到儀器 TCP 5025。
4. 選 Source、Sink 或 Battery，設定參數，按「套用並讀回驗證」。套用不會自動 ON。
5. 核對接線、額定值與實機面板後才按 ON。結束請用「OFF 並結束程式」。

本程式只監聽 `127.0.0.1`，不是 GitHub Pages 或雲端直接控制公司內網。EXE 未簽章，公司可能要求 IT 審核。

## v0.3.1 模式切換修正

針對實機紀錄：`EMUL PS2Q` 後立即 `*OPC?` 無回覆。本版不在模式切換中的連線上排入 `*OPC?`；送出一次 EMUL 後，等待、重連並依序讀回身分、模式、OFF 與新錯誤。最多等待 20 秒（受整體操作逾時限制），失敗不繼續設定，也不會 ON。其他設定完成後仍保留 `*OPC?` 驗證。

重連只接受原本同一儀器；若讀回 +305、不符模式或非 OFF，停止並記錄。此修正尚待實機測試，不能由原紀錄斷定韌體不支援 PS2Q。若仍失敗，請匯出除錯 JSON，並提供儀器面板當時顯示的模式；不要反覆 ON。

## v0.3.0 新增與修正

- 預填 IP `169.254.30.1`；記住 IP、通訊選項與參數。重新開啟只帶入表單，**不自動連線、設定或 ON**。
- 連線經 `*IDN?` 確認型號後，先逐筆記錄當時已存在的錯誤，顯示為來源未確認的警告；後續新錯誤仍停止操作。
- 避免先前 `MEAS:POW?` 留下的 `+305` 被誤當成本輪錯誤。不使用 `*CLS` 隱藏錯誤，不自動清除保護。
- 當串流完整且 OFF 得到回覆時，顯示已確認 OFF；逾時／不完整串流不拿舊回覆冒充 OFF 確認。
- 新增唯讀「診斷連線」：不啟動輪詢，禁止套用／ON；可逐條查詢，找出哪一條指令產生新 SCPI 錯誤。
- 通訊紀錄含毫秒時間與每次查詢耗時，保留最近 2000 行；支援匯出除錯 JSON。
- 新增獨立的 **Battery／實驗性軟體 ESR** 模式，詳見下節。

## 三種阻值／模式，請勿混用

### Source／Sink 基本控制

Source 設定電壓／電流上限，實際 CC/CV 由負載決定；選單只設定 ON/OFF 轉換偏好。
Sink CC 設定吸收電流；Sink CV 設定電壓與吸收電流限制。

### Sink CR：電子負載阻值

`FUNC RES` / `RES` / `RES?` 控制與讀回負載阻值，範圍 0.08–4000 Ω。只有 CR 模式按 I=V/R 吸收電流；這不是電池等效串聯內阻。範圍與設定同一 SCPI 訊息發送，避免切換量程衝突。

### Battery：實驗性軟體 ESR

此模式實作 **主機端、低速、準靜態** 模型：

```text
Vtarget = Voc − I × (ESR_mΩ / 1000)
正電流定義為放電，負電流為充電。
```

例如 Voc=4 V、ESR=10 mΩ：+1 A 放電時目標 3.99 V，−1 A 充電時目標 4.01 V。ESR 欄位以 mΩ 輸入，預填 10。

- 套用時要求儀器接受 `EMUL PS2Q`，並讀回模式、電壓、電流與 OVP；不支援就停止，**不以 PSUP/LOAD 輪流切換或 CR 冒充 ESR**。
- 沒有傳送未經文件確認的「原生 ESR」指令。ESR 是本程式的模型參數，不是硬體 ESR 暫存器讀回。
- ON 後，完成每輪 V/I 等查詢後更新電壓，間隔至少約 1 秒，加上 SCPI 與量測延遲可能更慢。每輪指令電壓最多改變 5 mV，低於約 0.65 mV 的變更不發送。
- **不是毫秒級即時模型，不適用高頻脈衝、快速充放電切換或高速控制穩定度驗證。**
- 必須先用低功率受控負載確認 PS2Q 的量測電流符號。使用者目前的 K-01.14.03-01.00-02.14-01.06 韌體是否接受 PS2Q、符號與動態性能，尚待實機驗證。
- `CURR` 是儀器電流設定，**不能據此宣稱已設定獨立硬體 Sink 限流**。本程式另做雙向軟體超流監控，不能取代硬體保護；首次測試不要接實體電池或未知條件充電器。
- 設定允許端電壓上下限與 OVP。計算或實測超出界線、外部控制端改變設定或發生新 SCPI 錯誤時，停止並要求 OFF。
- 不包含 SOC、容量、溫度模型、RC 支路或交流頻率相關阻抗。單一 ESR 不能代表完整電池阻抗。

本版應用限制為 30 V／20 A／200 W；軟體 ESR 額外限制為 0–1000 mΩ。這些是應用限制，不是儀器完整規格或精度保證。

## 明天的測試順序

請先閱讀 [明天測試清單](TOMORROW.md)。先診斷與 Source，再視結果測 Sink、CR；最後才測 PS2Q 與 ESR。測試前把預填值與你的設備額定值核對，不要直接把預填值視為適用 DUT。

若失敗，按「匯出除錯 JSON」，提供 `version`、`identity`、`last_failure`、`warning`、`diagnostics` 與相關 TX/RX。除錯檔包含儀器序號、IP 與參數，分享前可自行檢查；不包含本機工作階段密鑰。

## 本機資料

Windows 使用者資料夾：`%APPDATA%\E36731A-LAN-Control\`，包含 `preferences.json`、`debug.log`，以及跨次啟動輪替的 `debug.previous.log`。不需要管理員權限；寫入失敗時仍可使用，但無法保證跨次設定記憶／磁碟紀錄。

「儲存設定」只儲存表單。連線成功與套用成功亦會記住相關設定。程式不保存或自動恢復 ON 狀態。

## 量測與失效處理

功率是循序量測 V×I 的計算值，不等同儀器平均功率。不傳送已觀察到逾時的 `MEAS:POW?`。曲線／CSV 保留最近 120 筆，CSV 區分 CR 設定阻值、ESR 目標電壓及指令電壓。

網頁超過 10 秒未回報，程式會嘗試 OFF 並斷線；背景分頁節流可能觸發。強制結束 EXE、斷網、當機或關機無法保證 OFF。OFF 不是物理隔離，也不是硬體緊急停止。通訊不完整時只盡力發送 OFF，不冒充確認。

## 開發者

原始碼位於 [src](src)，僅使用 Go 標準函式庫；一般使用者不需安裝開發環境。

```sh
cd src
go test -race ./...
go vet ./...
node ui_test.cjs
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o E36731A-LAN-Control.exe .
```

`index.html` 內嵌於 EXE；修改後需重建。`ui_test.cjs` 是邏輯替身測試，不等於瀏覽器視覺驗證。

## 原廠依據與未確認項目

- [E36731A Programming Guide](https://www.keysight.com/us/en/assets/9923-01316/programming-guides/E36731A-Battery-Emulator-Programming-Guide.pdf)：E36731-90009，Edition 2，August 2026。第 64 頁 PS2Q、第 48 頁 CURR、第 145／148／149 頁電壓與 OVP、第 103–105 頁負載 RES。
- [原廠規格表](https://www.keysight.com/us/en/assets/3123-1042/data-sheets/E36731A-Battery-emulator-and-profiler.pdf)列有可程控輸出電阻；這不能單獨證明本程式已取得原生硬體 ESR 的 SCPI 控制方式。此項仍待 Keysight 或進一步文件確認。
- [原廠電池模擬軟體說明](https://helpfiles.keysight.com/Standalone_BenchVueSoftware_PW_HelpFiles/PWPowerAppSuite/Content/Battery%20Test%20and%20Emulation/Applications/Emulate_Function.htm)描述 Ri 與充放電模擬；本程式不是該軟體的替代認證版本。
