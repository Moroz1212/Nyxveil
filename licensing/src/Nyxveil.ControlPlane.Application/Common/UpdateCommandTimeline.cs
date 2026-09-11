using Nyxveil.ControlPlane.Application.Contracts.V1;
using Nyxveil.ControlPlane.Domain.Entities;
using Nyxveil.ControlPlane.Domain.Enums;

namespace Nyxveil.ControlPlane.Application.Common;

/// <summary>
/// Builds an UpdateNodeLatest timeline from real NodeCommand timestamps/phases only (no invented phases).
/// </summary>
public static class UpdateCommandTimeline
{
    public static IReadOnlyList<UpdateTimelineStep> Build(NodeCommand cmd)
    {
        if (cmd.Type != NodeCommandType.UpdateNodeLatest)
            return Array.Empty<UpdateTimelineStep>();

        var steps = new List<(string Id, string Title, DateTime? At, string? Msg, bool ForceReached)>();

        void Add(string id, string title, DateTime? at, string? msg = null, bool force = false) =>
            steps.Add((id, title, at, msg, force));

        Add("created", "Команда создана", cmd.CreatedAt);
        Add("issued", "Команда выдана", cmd.IssuedAt);

        var draining = string.Equals(cmd.ProgressPhase, "Draining", StringComparison.OrdinalIgnoreCase)
                       || cmd.ClaimedAt is not null
                       || cmd.StartedAt is not null
                       || cmd.CompletedAt is not null;
        Add("draining", "Node переведён в Drain",
            draining ? (cmd.ProgressUpdatedAt ?? cmd.ClaimedAt ?? cmd.StartedAt) : null,
            draining ? cmd.ProgressMessage : null,
            force: draining);

        var claimed = cmd.ClaimedAt is not null || cmd.StartedAt is not null || cmd.CompletedAt is not null;
        Add("claimed", "Команда получена node", cmd.ClaimedAt, force: claimed);

        var started = cmd.StartedAt is not null || cmd.CompletedAt is not null
                      || cmd.Status is NodeCommandStatus.Running or NodeCommandStatus.Executing;
        Add("started", "Обновление начато", cmd.StartedAt, force: started);

        var completedOk = cmd.Status == NodeCommandStatus.Succeeded
                          || string.Equals(cmd.ResultCode, "updated_healthy", StringComparison.OrdinalIgnoreCase);
        var completedFail = cmd.Status is NodeCommandStatus.Failed or NodeCommandStatus.Expired
                            || (!string.IsNullOrEmpty(cmd.ResultCode) && !completedOk && cmd.CompletedAt is not null);

        if (completedOk)
        {
            Add("terminal", "Terminal result получен", cmd.CompletedAt, cmd.ResultCode, force: true);
            Add("restored", "Node восстановлен (admin state)", cmd.CompletedAt, force: true);
        }
        else if (completedFail)
        {
            Add("terminal", "Terminal result получен", cmd.CompletedAt,
                string.IsNullOrWhiteSpace(cmd.ResultCode) ? cmd.ResultMessage : $"{cmd.ResultCode}: {cmd.ResultMessage}",
                force: true);
        }
        else if (string.Equals(cmd.ProgressPhase, "Completed", StringComparison.OrdinalIgnoreCase)
                 || string.Equals(cmd.ProgressPhase, "Failed", StringComparison.OrdinalIgnoreCase))
        {
            Add("progress", cmd.ProgressPhase ?? "Прогресс", cmd.ProgressUpdatedAt, cmd.ProgressMessage, force: true);
        }

        // Mark reached: any step with At or ForceReached, plus all prior steps once a later one is reached.
        var list = new List<UpdateTimelineStep>();
        var anyLater = false;
        for (var i = steps.Count - 1; i >= 0; i--)
        {
            var s = steps[i];
            var reached = s.ForceReached || s.At is not null || anyLater;
            if (reached) anyLater = true;
            list.Insert(0, new UpdateTimelineStep
            {
                Id = s.Id,
                Title = s.Title,
                Reached = reached,
                AtUtc = s.At,
                Message = s.Msg
            });
        }

        var currentIdx = list.FindLastIndex(s => s.Reached);
        if (currentIdx >= 0 && currentIdx < list.Count - 1 && cmd.CompletedAt is null)
        {
            // Current = first unreached after last reached, or last reached if running.
            for (var i = 0; i < list.Count; i++)
            {
                if (i == currentIdx + 1 || (i == currentIdx && list.All(x => x.Reached == list[i].Reached || i == list.Count - 1)))
                {
                    // mark current as the next incomplete, else last reached
                }
            }
            if (currentIdx + 1 < list.Count && !list[currentIdx + 1].Reached)
            {
                list[currentIdx + 1] = new UpdateTimelineStep
                {
                    Id = list[currentIdx + 1].Id,
                    Title = list[currentIdx + 1].Title,
                    Reached = false,
                    Current = true,
                    AtUtc = list[currentIdx + 1].AtUtc,
                    Message = list[currentIdx + 1].Message
                };
            }
            else
            {
                list[currentIdx] = new UpdateTimelineStep
                {
                    Id = list[currentIdx].Id,
                    Title = list[currentIdx].Title,
                    Reached = list[currentIdx].Reached,
                    Current = true,
                    AtUtc = list[currentIdx].AtUtc,
                    Message = list[currentIdx].Message
                };
            }
        }
        else if (currentIdx >= 0)
        {
            list[currentIdx] = new UpdateTimelineStep
            {
                Id = list[currentIdx].Id,
                Title = list[currentIdx].Title,
                Reached = list[currentIdx].Reached,
                Current = true,
                AtUtc = list[currentIdx].AtUtc,
                Message = list[currentIdx].Message
            };
        }

        return list;
    }
}
