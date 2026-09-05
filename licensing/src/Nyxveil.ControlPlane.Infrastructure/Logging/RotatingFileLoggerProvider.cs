using System.Collections.Concurrent;
using System.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Nyxveil.ControlPlane.Application.Options;

namespace Nyxveil.ControlPlane.Infrastructure.Logging;

/// <summary>Simple size-based rotating file logger for ProgramData.</summary>
public sealed class RotatingFileLoggerProvider : ILoggerProvider
{
    private readonly IOptionsMonitor<FileLoggingOptions> _options;
    private readonly ConcurrentDictionary<string, RotatingFileLogger> _loggers = new(StringComparer.Ordinal);
    private readonly object _writeLock = new();
    private StreamWriter? _writer;
    private string? _currentPath;
    private long _currentSize;

    public RotatingFileLoggerProvider(IOptionsMonitor<FileLoggingOptions> options)
    {
        _options = options;
    }

    public ILogger CreateLogger(string categoryName) =>
        _loggers.GetOrAdd(categoryName, name => new RotatingFileLogger(name, this));

    internal void Write(string category, LogLevel level, string message)
    {
        var opts = _options.CurrentValue;
        if (!opts.Enabled)
            return;

        var dir = ResolveDirectory(opts);
        Directory.CreateDirectory(dir);

        lock (_writeLock)
        {
            EnsureWriter(dir, opts);
            var line = $"{DateTimeOffset.UtcNow:O} [{level}] {category}: {message}{Environment.NewLine}";
            var bytes = Encoding.UTF8.GetByteCount(line);
            _writer!.Write(line);
            _writer.Flush();
            _currentSize += bytes;

            var maxBytes = Math.Max(1, opts.MaxFileSizeMB) * 1024L * 1024L;
            if (_currentSize >= maxBytes)
                Rotate(dir, opts);
        }
    }

    private void EnsureWriter(string dir, FileLoggingOptions opts)
    {
        if (_writer is not null)
            return;

        var path = Path.Combine(dir, $"{opts.FilePrefix}-{DateTime.UtcNow:yyyyMMdd}.log");
        _currentPath = path;
        var exists = File.Exists(path);
        _writer = new StreamWriter(new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite), Encoding.UTF8);
        _currentSize = exists ? new FileInfo(path).Length : 0;
    }

    private void Rotate(string dir, FileLoggingOptions opts)
    {
        _writer?.Dispose();
        _writer = null;

        if (!string.IsNullOrEmpty(_currentPath) && File.Exists(_currentPath))
        {
            var rotated = Path.Combine(
                dir,
                $"{opts.FilePrefix}-{DateTime.UtcNow:yyyyMMdd-HHmmss}.log");
            File.Move(_currentPath, rotated, overwrite: true);
        }

        CleanupOldFiles(dir, opts);
        EnsureWriter(dir, opts);
    }

    private static void CleanupOldFiles(string dir, FileLoggingOptions opts)
    {
        var retain = Math.Max(1, opts.MaxRetainedFiles);
        var files = Directory.GetFiles(dir, $"{opts.FilePrefix}-*.log")
            .Select(f => new FileInfo(f))
            .OrderByDescending(f => f.LastWriteTimeUtc)
            .Skip(retain)
            .ToList();
        foreach (var f in files)
        {
            try { f.Delete(); } catch { /* best effort */ }
        }
    }

    private static string ResolveDirectory(FileLoggingOptions opts)
    {
        if (!string.IsNullOrWhiteSpace(opts.Directory))
            return Environment.ExpandEnvironmentVariables(opts.Directory);

        return Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
            "Nyxveil",
            "ControlPlane",
            "logs");
    }

    public void Dispose()
    {
        lock (_writeLock)
        {
            _writer?.Dispose();
            _writer = null;
        }
    }

    private sealed class RotatingFileLogger : ILogger
    {
        private readonly string _category;
        private readonly RotatingFileLoggerProvider _provider;

        public RotatingFileLogger(string category, RotatingFileLoggerProvider provider)
        {
            _category = category;
            _provider = provider;
        }

        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => logLevel != LogLevel.None;

        public void Log<TState>(
            LogLevel logLevel,
            EventId eventId,
            TState state,
            Exception? exception,
            Func<TState, Exception?, string> formatter)
        {
            if (!IsEnabled(logLevel))
                return;

            var message = formatter(state, exception);
            if (exception is not null)
                message += Environment.NewLine + exception;
            _provider.Write(_category, logLevel, message);
        }
    }
}
