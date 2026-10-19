# Custom PyInstaller hook override for webrtcvad / webrtcvad-wheels
# Avoids PyInstaller ImportErrorWhenRunningHook on webrtcvad-wheels metadata
hiddenimports = ['webrtcvad']
